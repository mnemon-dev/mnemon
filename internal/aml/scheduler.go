package aml

import (
	"container/heap"
	"container/list"
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"runtime/debug"
	"sync"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// Background consolidation (ConsolidationBackground) runs the users' jobs in
// a fixed pool of workers. Raw records and each user's timelines state say
// what is pending; the scheduler only remembers which users to look at, how
// much each has pending (its backlog, in the characters a job reads), and who
// runs next: the first user of a FIFO queue that no Search holds. A user is
// idle, queued, running or backing off after a failed job, and a Search may
// hold it in any of these states; one job per user runs at a time.

const (
	// defaultConsolidationWorkers is how many users consolidate at once.
	defaultConsolidationWorkers = 4
	// maxConsolidationUsers bounds the users with work (queued, running or
	// backing off); a notification beyond it is dropped.
	maxConsolidationUsers = 100000
	// A failed job's user is retried after retryBase × 2ⁿ⁻¹ for its n-th
	// failure in a row, ±20%, at most retryMax less up to 20% (retryDelay); a
	// job the budget reserve stopped after reservePause.
	retryBase    = 30 * time.Second
	retryMax     = 10 * time.Minute
	reservePause = 10 * time.Minute
	// queueFullEvery spaces the logs of dropped notifications.
	queueFullEvery = time.Minute
)

// SchedulerStatus is a snapshot of the scheduler: the users queued (held
// ones among them), running and backing off, the users a Search holds, how
// long the oldest work not yet consolidated has waited (queued, running or
// backing off since the notification a successful job has not covered), and
// the largest backlog, in characters.
//
// In /admin/status (AdminStatus.Queue) its fields are queued, running,
// backingOff, held, oldestQueued (nanoseconds) and maxBacklog.
type SchedulerStatus struct {
	Queued       int           `json:"queued"`
	Running      int           `json:"running"`
	BackingOff   int           `json:"backingOff"`
	Held         int           `json:"held"`
	OldestQueued time.Duration `json:"oldestQueued"`
	MaxBacklog   int64         `json:"maxBacklog"`
}

// How a job ended, as jobDone is told: it succeeded, failed (the user backs
// off), was paused by the budget reserve (it backs off for longer), or was cut
// by the end of the scheduler (the user is queued again).
const (
	jobOK       = "ok"
	jobFailed   = "failed"
	jobPaused   = "paused"
	jobCanceled = "canceled"
)

var jobOutcomes = []string{jobOK, jobFailed, jobPaused, jobCanceled}

// jobOutcome is how a job that failed with err ended, cut says whether the
// scheduler's end had cut it.
func jobOutcome(cut bool, err error) string {
	switch {
	case cut:
		return jobCanceled
	case err == nil:
		return jobOK
	case errors.Is(err, memoryagent.ErrBackgroundReserve):
		return jobPaused
	}
	return jobFailed
}

type userState uint8

const (
	userIdle userState = iota
	userQueued
	userRunning
	userBackingOff
)

// schedUser is one user the scheduler knows: while it has work, is held or
// waited on, or, idle, has characters pending (the tail a held job leaves).
type schedUser struct {
	key   string
	state userState
	elem  *list.Element // its place in the queue, while queued
	// holds counts the Searches holding the user, waiters the WaitBacklog
	// calls waiting on it.
	holds, waiters int
	// backlog is the characters pending: the last job's measure, plus what
	// Adds wrote since; added is what they wrote while it ran, which the
	// measure may miss; dirty says Adds wrote while it ran. measured says a
	// job has ended with a measure of the backlog since the scheduler first
	// knew the user: before that backlog is only what Adds have told it, and
	// 0 for a user it was told nothing of.
	backlog, added int64
	dirty          bool
	measured       bool
	// failures counts its failed jobs in a row; retryAt ends its backoff,
	// and seq orders retries due at the same time.
	failures int
	retryAt  time.Time
	seq      uint64
	// since is when the oldest notification no successful job has covered
	// came (zero when idle); dirtySince, the first while it ran.
	since, dirtySince time.Time
}

// scheduler runs the users' consolidation jobs (run) on its workers. Its
// callbacks run without its lock: run may block until its context ends and
// may call the scheduler; logf and now must not block. logf is given the
// context of the work a line is about: a request's for a notification it
// made, a job's for its own lines.
type scheduler struct {
	workers, maxUsers int
	// run is a user's job; it returns the user's backlog measured after the
	// job (negative when unknown) and the job's failure.
	run  func(ctx context.Context, key string) (backlog int64, err error)
	logf func(ctx context.Context, format string, args ...any)
	now  func() time.Time
	// jitter is uniform in [0, 1): the backoff's jitter.
	jitter func() float64
	// jobDone, if set, is told how each job ended (jobOutcomes) before the
	// scheduler's state tells of it. It runs without the lock and must not
	// block.
	jobDone func(outcome string)

	mu    sync.Mutex
	users map[string]*schedUser
	// measured holds the users a job or an Add has measured since the
	// scheduler was made, forgotten ones too (HasMeasured): one key per user
	// store, as many as the data root holds.
	measured map[string]bool
	queue    *list.List // of *schedUser, queued in order
	retries  retryHeap  // the users backing off, the next retry first
	running  int        // users running
	retrySeq uint64
	changed  chan struct{} // closed, and replaced, at every change
	started  bool          // Run is running
	stopped  bool          // Run returned: nothing runs, no Add waits
	// draining says Drain was called: no job starts and no Add waits from
	// then on, while the jobs running go on to their end.
	draining   bool
	fullLogged time.Time
}

func newScheduler(workers, maxUsers int, run func(ctx context.Context, key string) (backlog int64, err error), logf func(ctx context.Context, format string, args ...any), now func() time.Time) *scheduler {
	return &scheduler{workers: workers, maxUsers: maxUsers, run: run, logf: logf, now: now, jitter: rand.Float64,
		users: map[string]*schedUser{}, measured: map[string]bool{}, queue: list.New(), changed: make(chan struct{})}
}

// short is a user key as logs name it: its 8-hex-digit prefix.
func short(key string) string { return key[:min(8, len(key))] }

// Notify tells the scheduler that an Add wrote addedChars characters (0 for
// none: a job left work, or a Search's flush did) for the user; ctx is the
// work's that tells it, a request's or a job's. Its backlog grows by them
// and an idle user joins the back of the queue; a queued user keeps its
// place, one backing off its retry time, and one running runs again after
// its job. Past maxUsers users with work, the notification is dropped and
// logged at most once per minute, on ctx (with the request's id); the
// user's next Add or Search picks it up.
func (s *scheduler) Notify(ctx context.Context, key string, addedChars int64) {
	now := s.now()
	s.mu.Lock()
	u := s.users[key]
	if (u == nil || u.state == userIdle) && s.activeLocked() >= s.maxUsers {
		logFull := s.fullLogged.IsZero() || now.Sub(s.fullLogged) >= queueFullEvery
		if logFull {
			s.fullLogged = now
		}
		s.mu.Unlock()
		if logFull {
			s.logf(ctx, "consolidation queue full: %d users have work; dropped a notification (user %s)", s.maxUsers, short(key))
		}
		return
	}
	if u == nil {
		u = &schedUser{key: key}
		s.users[key] = u
	}
	u.backlog += addedChars
	switch u.state {
	case userIdle:
		u.since = now
		s.enqueueLocked(u)
	case userRunning:
		u.added += addedChars
		if !u.dirty {
			u.dirty, u.dirtySince = true, now
		}
	}
	s.unlockChanged()
}

// WaitBacklog returns nil once the user's backlog is at most limit, or once
// the scheduler has stopped or is draining or the user's consolidation backs
// off after a failed job (no job would lower it before then), and ctx's error
// if ctx ends first. A user the scheduler does not know has no backlog.
// Waiting on an idle user queues a job, which measures its backlog, unless no
// job can be queued.
func (s *scheduler) WaitBacklog(ctx context.Context, key string, limit int64) error {
	now := s.now()
	s.mu.Lock()
	u := s.users[key]
	if u == nil {
		s.mu.Unlock()
		return nil
	}
	u.waiters++
	defer func() {
		u.waiters--
		s.forgetLocked(u)
		s.mu.Unlock()
	}()
	for u.backlog > limit && u.state != userBackingOff && !s.stopped && !s.draining {
		var old chan struct{}
		if u.state == userIdle {
			if s.activeLocked() >= s.maxUsers {
				return nil
			}
			u.since = now
			s.enqueueLocked(u)
			old = s.changedLocked()
		}
		changed := s.changed
		s.mu.Unlock()
		if old != nil {
			close(old)
		}
		select {
		case <-changed:
		case <-ctx.Done():
		}
		s.mu.Lock()
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

// Hold keeps the user's jobs from starting until release is called, while a
// Search flushes it: a queued user keeps its place in the queue and is
// passed over; a job already running goes on. release may be called more
// than once.
func (s *scheduler) Hold(key string) (release func()) {
	s.mu.Lock()
	u := s.users[key]
	if u == nil {
		// Holds are bounded by the Searches in flight, not by maxUsers.
		u = &schedUser{key: key}
		s.users[key] = u
	}
	u.holds++
	s.mu.Unlock()
	return sync.OnceFunc(func() {
		s.mu.Lock()
		u.holds--
		s.forgetLocked(u)
		s.unlockChanged()
	})
}

// yielding reports whether the user's running job should end before its next
// consolidation job: a Search holds the user, or the scheduler is draining.
func (s *scheduler) yielding(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[key]
	return s.draining || u != nil && u.holds > 0
}

// Drain is the first step of a graceful stop: no job starts from now on,
// and no Add waits for consolidation (WaitBacklog returns), while the jobs
// running go on to their end; the workers exit once theirs has ended, so Run
// returns then. Run's context ending still cuts a running job. Notifications
// keep queueing users, for no job. Drain may be called again.
func (s *scheduler) Drain() {
	s.mu.Lock()
	s.draining = true
	s.unlockChanged()
}

// WaitDrained drains the scheduler (Drain) and returns nil once no job runs;
// if ctx ends first, a drainError wrapping ctx's error.
func (s *scheduler) WaitDrained(ctx context.Context) error {
	s.Drain()
	for {
		s.mu.Lock()
		running, changed := s.running, s.changed
		s.mu.Unlock()
		if running == 0 {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return drainError{running: s.runningJobs(), cause: ctx.Err()}
		}
	}
}

// drainError says how many jobs still ran when WaitDrained's context ended;
// it wraps that context's error.
type drainError struct {
	running int
	cause   error
}

func (e drainError) Error() string {
	return fmt.Sprintf("consolidation jobs still running: %d", e.running)
}

func (e drainError) Unwrap() error { return e.cause }

// runningJobs counts the jobs running.
func (s *scheduler) runningJobs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Run runs the workers until ctx ends, or until a drain (Drain) and the
// jobs running then have ended, and returns once every worker has exited. A
// job running when ctx ends sees it end, and its user, cut, is queued again;
// its state is saved per batch, so the job resumes later. After Run returns
// no job starts and no Add waits. One Run runs at a time.
func (s *scheduler) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("the consolidation scheduler is already running")
	}
	s.started, s.stopped = true, false
	s.mu.Unlock()
	var workers sync.WaitGroup
	for range s.workers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			s.work(ctx)
		}()
	}
	workers.Wait()
	s.mu.Lock()
	s.started, s.stopped = false, true
	s.unlockChanged()
	return nil
}

// Status is a snapshot of the scheduler.
func (s *scheduler) Status() SchedulerStatus {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	status := SchedulerStatus{Queued: s.queue.Len(), Running: s.running, BackingOff: len(s.retries)}
	for _, u := range s.users {
		if u.holds > 0 {
			status.Held++
		}
		status.MaxBacklog = max(status.MaxBacklog, u.backlog)
		if u.state != userIdle && !u.since.IsZero() {
			status.OldestQueued = max(status.OldestQueued, now.Sub(u.since))
		}
	}
	return status
}

// StateOf is the user's consolidation as the store overview shows it:
// "queued", "running", "backing-off" (after failed jobs; failures says how
// many in a row), "held" (idle, held by a Search's flush), or "" for idle.
func (s *scheduler) StateOf(key string) (state string, failures int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[key]
	if u == nil {
		return "", 0
	}
	switch u.state {
	case userQueued:
		state = "queued"
	case userRunning:
		state = "running"
	case userBackingOff:
		state = "backing-off"
	default:
		if u.holds > 0 {
			state = "held"
		}
	}
	return state, u.failures
}

// Backlog is the characters the scheduler estimates the user has pending: the
// measure of the user's last job plus what Adds have written since. It has no
// estimate (known false) for a user whose job has not ended with a measure yet,
// and for one it has dropped, which has nothing pending.
func (s *scheduler) Backlog(key string) (chars int64, known bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[key]
	if u == nil || !u.measured {
		return 0, false
	}
	return u.backlog, true
}

// Measured records a measure of the user's backlog taken outside a job, by
// an Add of a user the scheduler had no measure of (Backlog unknown: after a
// start, or after a Search's hold made the user known; the final review's
// parked T6-a): the estimate is that measure from then on, plus what Adds
// write after it. A user it has no entry for, one a job has measured, and
// one whose job runs, which measures at its end, keep their estimate.
func (s *scheduler) Measured(key string, chars int64) {
	s.mu.Lock()
	u := s.users[key]
	if u == nil || u.measured || u.state == userRunning {
		s.mu.Unlock()
		return
	}
	u.backlog, u.measured = chars, true
	s.measured[key] = true
	s.unlockChanged()
}

// HasMeasured reports whether a job or an Add has measured the user's
// backlog since the scheduler was made, also when the scheduler has
// forgotten the user since (a measure of 0): its estimate is then right from
// what Adds tell it, and an Add need not measure it again (Server's
// measureUnknown): one measure per user and start.
func (s *scheduler) HasMeasured(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.measured[key]
}

// signal makes the workers look again, as after any change: a clock moved
// by a test, say.
func (s *scheduler) signal() {
	s.mu.Lock()
	s.unlockChanged()
}

// work runs jobs until ctx ends or the scheduler drains.
func (s *scheduler) work(ctx context.Context) {
	for {
		u := s.next(ctx)
		if u == nil {
			return
		}
		backlog, err := s.call(ctx, u.key)
		s.finish(ctx, u, backlog, err)
	}
}

// next waits for the next user to run and marks it running: the first user
// of the queue no Search holds, once the users whose backoff has ended have
// joined the back of the queue. It returns nil once ctx has ended or the
// scheduler drains.
func (s *scheduler) next(ctx context.Context) *schedUser {
	for ctx.Err() == nil {
		// The change channel is taken before the clock is read: a change
		// after the reading (a moved clock, a signal) then ends the wait.
		s.mu.Lock()
		changed := s.changed
		s.mu.Unlock()
		now := s.now()
		s.mu.Lock()
		if s.draining {
			s.mu.Unlock()
			return nil
		}
		s.retryDueLocked(now)
		if u := s.firstUnheldLocked(); u != nil {
			s.queue.Remove(u.elem)
			u.elem, u.state = nil, userRunning
			u.added, u.dirty, u.dirtySince = 0, false, time.Time{}
			s.running++
			s.mu.Unlock()
			return u
		}
		wait := time.Duration(0)
		if len(s.retries) > 0 {
			wait = s.retries[0].retryAt.Sub(now)
		}
		s.mu.Unlock()
		sleep(ctx, changed, wait)
	}
	return nil
}

// sleep waits until ctx ends, changed is closed or, when wait is positive,
// wait has passed.
func sleep(ctx context.Context, changed <-chan struct{}, wait time.Duration) {
	var timeout <-chan time.Time
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case <-ctx.Done():
	case <-changed:
	case <-timeout:
	}
}

// call runs the user's job; a job that panics fails, its panic and stack
// logged.
func (s *scheduler) call(ctx context.Context, key string) (backlog int64, err error) {
	defer func() {
		if value := recover(); value != nil {
			s.logf(ctx, "consolidation job panicked (user %s): %v\n%s", short(key), value, debug.Stack())
			backlog, err = -1, fmt.Errorf("panic: %v", value)
		}
	}()
	return s.run(ctx, key)
}

// finish settles u's job, which measured backlog (negative when unknown)
// and failed with err. A job ctx's end cut queues its user again. After a
// successful job its user is idle, or queued again when Adds wrote while it
// ran; after a failed one it backs off (retryDelay), for reservePause when
// the budget reserve stopped it.
func (s *scheduler) finish(ctx context.Context, u *schedUser, backlog int64, err error) {
	now, jitter, cut := s.now(), s.jitter(), ctx.Err() != nil
	if s.jobDone != nil {
		s.jobDone(jobOutcome(cut, err))
	}
	s.mu.Lock()
	s.running--
	if backlog >= 0 {
		u.backlog, u.measured = backlog+u.added, true
		s.measured[u.key] = true
	}
	u.added = 0
	var line string
	switch {
	case cut:
		s.enqueueLocked(u)
	case err == nil:
		u.failures = 0
		if u.dirty {
			u.since = u.dirtySince
			s.enqueueLocked(u)
		} else {
			u.state, u.since = userIdle, time.Time{}
		}
	default:
		u.failures++
		delay := retryDelay(u.failures, jitter)
		line = fmt.Sprintf("consolidation retries in %s (user %s)", delay.Round(time.Second), short(u.key))
		if errors.Is(err, memoryagent.ErrBackgroundReserve) {
			delay, line = reservePause, fmt.Sprintf("consolidation paused: budget reserve (user %s)", short(u.key))
		}
		s.retrySeq++
		u.state, u.retryAt, u.seq = userBackingOff, now.Add(delay), s.retrySeq
		heap.Push(&s.retries, u)
	}
	u.dirty, u.dirtySince = false, time.Time{}
	s.forgetLocked(u)
	s.unlockChanged()
	if line != "" {
		s.logf(ctx, "%s", line)
	}
}

// retryDelay is the wait before a user's retry after its failures-th failed
// job in a row: retryBase × 2^(failures−1), times 0.8 + 0.4 × jitter (±20%
// for a jitter in [0, 1)), at most retryMax × (0.8 + 0.2 × jitter). The cap
// is jittered too (the final review's parked T6-d), so that users which keep
// failing do not all retry at exactly retryMax, in step.
func retryDelay(failures int, jitter float64) time.Duration {
	delay := retryMax
	if failures <= 10 { // past ten, the doubling alone passes retryMax
		delay = retryBase << max(failures-1, 0)
	}
	jittered := time.Duration(math.Round(float64(delay) * (0.8 + 0.4*jitter)))
	return min(jittered, time.Duration(math.Round(float64(retryMax)*(0.8+0.2*jitter))))
}

// activeLocked counts the users with work: queued, running or backing off.
func (s *scheduler) activeLocked() int { return s.queue.Len() + s.running + len(s.retries) }

// enqueueLocked puts u at the back of the queue.
func (s *scheduler) enqueueLocked(u *schedUser) {
	u.state, u.elem = userQueued, s.queue.PushBack(u)
}

// retryDueLocked moves the users whose backoff has ended to the back of the
// queue, the earliest first.
func (s *scheduler) retryDueLocked(now time.Time) {
	for len(s.retries) > 0 && !s.retries[0].retryAt.After(now) {
		s.enqueueLocked(heap.Pop(&s.retries).(*schedUser))
	}
}

// firstUnheldLocked is the first queued user no Search holds, or nil.
func (s *scheduler) firstUnheldLocked() *schedUser {
	for element := s.queue.Front(); element != nil; element = element.Next() {
		if u := element.Value.(*schedUser); u.holds == 0 {
			return u
		}
	}
	return nil
}

// forgetLocked drops u once nothing needs it: idle with nothing pending,
// not held and not waited on.
func (s *scheduler) forgetLocked(u *schedUser) {
	if u.state == userIdle && u.holds == 0 && u.waiters == 0 && u.backlog <= 0 && s.users[u.key] == u {
		delete(s.users, u.key)
	}
}

// changedLocked replaces the change channel and returns the old one, to be
// closed once mu is released: closing it wakes every worker and waiter. They
// are few, the workers and the Adds waiting at the bound (at most the Adds
// served at once), so one channel for all costs little; per-user channels
// would pay only if waiters grew far beyond that (the final review's parked
// T6-g).
func (s *scheduler) changedLocked() chan struct{} {
	old := s.changed
	s.changed = make(chan struct{})
	return old
}

// unlockChanged releases mu after a change and wakes those waiting for one.
func (s *scheduler) unlockChanged() {
	old := s.changedLocked()
	s.mu.Unlock()
	close(old)
}

// retryHeap orders the users backing off by retry time, then by when they
// failed.
type retryHeap []*schedUser

func (h retryHeap) Len() int { return len(h) }

func (h retryHeap) Less(i, j int) bool {
	if !h[i].retryAt.Equal(h[j].retryAt) {
		return h[i].retryAt.Before(h[j].retryAt)
	}
	return h[i].seq < h[j].seq
}

func (h retryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *retryHeap) Push(x any) { *h = append(*h, x.(*schedUser)) }

func (h *retryHeap) Pop() any {
	old := *h
	u := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	return u
}
