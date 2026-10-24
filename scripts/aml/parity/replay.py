#!/usr/bin/env python3
"""Parity replay of aml-serve over HTTP: one request sequence, two builds, every body compared byte for byte.

The same request sequence goes to two builds, one after the other, and every Add body and every Search body is saved and
byte-compared.

  old = the baseline build (51cc7da5, before the serving hardening), `--consolidate-on-add`
        (live providers; records every model reply in the shared model cache)
  new = the build under test, `--consolidate background` (an admin listener on loopback and a throwaway admin token;
        replays from the same model cache, so it must make 0 paid calls)

Both read the same BM25-only configuration (deploy/aml/memory-config.industrial.json; only the budget file, the caps and
the model-cache path are local). Nothing else is shared but the datasets and the model cache.

The request sequence speaks the AML contract the way the platform does: Adds split at 20 messages or 2,000 words, Unix-ms
timestamps, user/assistant roles, a retried last Add, then every question of the conversation with top_k 100. LoCoMo's
speakers become user (speaker_a) and assistant (speaker_b), each content prefixed with the speaker's name; BEAM's question
carries "Current date: ..." as the earlier harness sends it. Searches arrive after the Adds that precede them, as the
dataset orders them (all Adds of a user, then its questions, four at a time), without waiting for any background
consolidation.

Stages (see README.md beside this file):

  plan                          the requests, counted and hashed; no server
  smoke                         start each build on throwaway paths and make no request
  run old|new                   one build over the whole sequence with the live providers (keys from the environment)
  replay                        the new build from the model cache only, then compare it with the recorded old run:
                                placeholder keys and dead proxies, so a cache miss cannot reach a provider
  compare                       two runs' saved bodies, consolidated state and paid calls
  scrub                         look for the provider keys in every file of the task directory

Money (a live run): the configuration's budget file and caps (DeepSeek CNY 15, JEV USD 5), on a budget file made for the
task, and a guard that stops the run at 80% of either. Keys: a live run reads DEEPSEEK_API_KEY and TYPESAFE_API_KEY from
its environment into the server's environment only; the client key and the admin token are throwaway values generated per
run. `replay` never reads a key. Nothing here prints, logs or writes a key.

Paths are arguments: --root is the task directory (the runs, the model cache and budget file in its private/, the
configuration), --inputs the six dataset files, --binary a build. `--dry` swaps in local models that fail at once, a private
output directory (ROOT/dry) and no keys: a plumbing test that spends nothing.
"""
import argparse, collections, concurrent.futures, datetime, hashlib, json, os, pathlib, re, secrets, shutil, signal, socket, sqlite3, \
    subprocess, sys, tempfile, threading, time, urllib.error, urllib.request

REPO = pathlib.Path(__file__).resolve().parents[3]      # scripts/aml/parity/replay.py, in the repository
DEFAULT_CONFIG = REPO / 'deploy/aml/memory-config.industrial.json'
# Set from the command line (main): the task directory, the directory of the dataset files, the shipped configuration.
ROOT = None
INPUTS = None
CONFIG = DEFAULT_CONFIG
CAPS = {'deepseek': 15, 'jev': 5}
SEARCH_WORKERS = 4
TOP_K = 100
FAULT = re.compile(r'degraded|failed|dropped|without JEV|panick|stopped with|left native|undecided|gave up|outran|consolidation skipped|level=ERROR|level=WARN', re.I)
STATUS_LINE = re.compile(r'status: adds \d+')
PROXY_VARS = ('HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'http_proxy', 'https_proxy', 'all_proxy')
PASSTHROUGH = ('PATH', 'HOME', 'USER', 'LOGNAME', 'LANG', 'LC_ALL', 'TMPDIR', 'NO_PROXY', 'no_proxy') + PROXY_VARS   # the proxy variables are how the providers are reached from some machines
DEAD_PROXY = 'http://127.0.0.1:9'      # `replay`: a loopback port nothing listens on
PLACEHOLDER_KEYS = {'DEEPSEEK_API_KEY': 'parity-replay-placeholder', 'TYPESAFE_API_KEY': 'parity-replay-placeholder'}


def say(**fields):
    print(json.dumps(fields, ensure_ascii=False), flush=True)


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, 'rb') as f:
        for chunk in iter(lambda: f.read(1 << 20), b''):
            h.update(chunk)
    return h.hexdigest()


def dump(path, value):
    path = pathlib.Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + '.tmp')
    tmp.write_text(json.dumps(value, ensure_ascii=False, indent=1) + '\n')
    os.replace(tmp, path)


# ---- the dataset side: the platform's Add split and the two conversations -------------------------------------------------------

def read_input(name):
    """One of the six dataset files of --inputs, parsed."""
    path = INPUTS / name
    if not path.is_file():
        raise SystemExit(f'{path} is missing: --inputs must hold {", ".join(DATASET_FILES)} (see README.md)')
    return json.loads(path.read_text())

def split(messages):
    """The platform's Add split: at most 20 messages and 2,000 words per request."""
    parts, cur, words = [], [], 0
    for msg in messages:
        n = len(msg['content'].split())
        if cur and (len(cur) == 20 or words + n > 2000):
            parts.append(cur); cur, words = [], 0
        cur.append(msg); words += n
    if cur:
        parts.append(cur)
    return parts


def locomo_sessions(ci):
    conv = read_input('locomo10.json')[ci]['conversation']
    a = conv['speaker_a']
    out = []
    for k in sorted((k for k in conv if re.fullmatch(r'session_\d+', k) and isinstance(conv[k], list)), key=lambda k: int(k.split('_')[1])):
        when = datetime.datetime.strptime(conv[k + '_date_time'], '%I:%M %p on %d %B, %Y').replace(tzinfo=datetime.timezone.utc)
        ms = int(when.timestamp() * 1000)
        msgs = [dict(role='user' if t['speaker'] == a else 'assistant',
                     content=f"{t['speaker']}: {t['text']}" + (f" [shares a photo: {t['blip_caption']}]" if t.get('blip_caption') else ''),
                     timestamp=ms + i * 1000) for i, t in enumerate(conv[k])]
        out.append((k, msgs))
    return out


def beam_sessions(corpus):
    raw = read_input('beam-128k.cases.json')
    src = next(c for c in raw if c['id'] == corpus)
    out = []
    for s in sorted(src['sessions'], key=lambda s: s['iso']):
        ms = int(datetime.datetime.fromisoformat(s['iso'].replace('Z', '+00:00')).timestamp() * 1000)
        out.append((s['id'], [dict(role=t['speaker'], content=t['text'], timestamp=ms + i * 1000) for i, t in enumerate(s['turns'])]))
    return out


def locomo_questions(ci):
    """All standard questions of the conversation (the harness's frozen cases; category 5 is not asked, as in the paper's 1,540)."""
    cases = read_input(f'locomo-{ci}.questions.json')['cases']
    sample = set(read_input('locomo-sample.json')['ids'])
    return [dict(id=c['id'], query=c['input']['query'], inSample=c['id'] in sample) for c in cases]


def beam_questions(corpus):
    """All probing questions of the conversation, each with the "Current date" line the earlier harness sends."""
    case = read_input(f'{corpus}.questions.json')
    sample = set(read_input('beam-sample.json')['ids'])
    return [dict(id=q['id'], query=f"Current date: {q['date']}\n{q['question']}", inSample=q['id'] in sample) for q in case['questions']]


DATASET_FILES = ['locomo10.json', 'beam-128k.cases.json', 'locomo-0.questions.json', 'locomo-sample.json',
                 'beam128k-1.questions.json', 'beam-sample.json']
USERS = ['locomo-0', 'beam128k-1']


def build_requests(user, max_sessions=None, max_searches=None):
    """The user's request sequence, in the order the dataset gives it: its Adds, the retry of the last Add, its Searches."""
    sessions, questions = (locomo_sessions(0), locomo_questions(0)) if user == 'locomo-0' else (beam_sessions(user), beam_questions(user))
    if max_sessions:
        sessions = sessions[:max_sessions]
    if max_searches:
        questions = questions[:max_searches]
    reqs, last = [], None
    for sid, msgs in sessions:
        for pi, part in enumerate(split(msgs)):
            last = dict(request_id=f'{user}:{sid}:{pi}', messages=part, user_id=user, session_id=sid)
            reqs.append(dict(kind='add', path='/add', body=json.dumps(last).encode()))
    reqs.append(dict(kind='add-retry', path='/add', body=json.dumps(last).encode()))   # the platform retries a failed Add with the same request_id
    for q in questions:
        reqs.append(dict(kind='search', path='/search', qid=q['id'], inSample=q['inSample'],
                         body=json.dumps(dict(query=q['query'], user_id=user, top_k=TOP_K)).encode()))
    for i, r in enumerate(reqs):
        r['seq'] = i
        r['reqSha256'] = sha256(r['body'])
    return reqs


# ---- configuration -------------------------------------------------------------------------------------------------------------

class Layout:
    """Where one invocation reads and writes: the task directory, or a private subdirectory for --dry."""
    def __init__(self, dry):
        self.dry = dry
        self.root = ROOT / 'dry' if dry else ROOT
        self.private = self.root / 'private'
        self.budget = self.private / 'model-budget.db'
        self.cache = self.private / 'model-cache.db'
        self.config = self.root / 'memory-config.json'

    def make_private(self):
        self.private.mkdir(parents=True, exist_ok=True)
        os.chmod(self.private, 0o700)


def effective_config(layout):
    """The shipped BM25-only configuration with only local paths changed: the budget file, its caps and the shared model cache."""
    if layout.dry:
        model = dict(kind='local', model='dummy', baseURL='http://127.0.0.1:9/v1', timeoutSeconds=5)
        c = dict(version=1, strategy='simple', view=dict(maxItems=100, maxCharacters=240000, order='time'), consolidation=dict(mode='layered'),
                 planner=model, judge=model, consolidator=model)
    else:
        if not CONFIG.is_file():
            raise SystemExit(f'{CONFIG}: no such configuration; pass --config (the default is the repository\'s deploy/aml/memory-config.industrial.json)')
        c = json.loads(CONFIG.read_text())
    assert 'embedding' not in c, 'the configuration must be BM25-only'
    c['budgetFile'], c['budgets'], c['modelCache'] = str(layout.budget), dict(CAPS), str(layout.cache)
    return c


def write_config(layout):
    layout.make_private()
    c = effective_config(layout)
    text = json.dumps(c, indent=2) + '\n'
    if layout.config.exists() and layout.config.read_text() != text:
        raise SystemExit(f'{layout.config} differs from the configuration this script would write; refusing to run on a changed configuration')
    layout.config.write_text(text)
    return c


# ---- the budget file -----------------------------------------------------------------------------------------------------------

def budget_read(path):
    """(accounts, attempts) of a budget file, read-only; ({}, {}) before it exists."""
    if not pathlib.Path(path).exists():
        return {}, {}
    last = None
    for _ in range(40):
        try:
            db = sqlite3.connect(f'file:{path}?mode=ro', uri=True, timeout=15)
            try:
                accounts = {k: dict(cap=c, spent=s, held=h) for k, c, s, h in db.execute('select kind, cap, spent, held from accounts')}
                attempts = {i: dict(kind=k, reserved=r, actual=a, inputTokens=it, outputTokens=ot)
                            for i, k, r, a, it, ot in db.execute('select id, kind, reserved, actual, input_tokens, output_tokens from attempts')}
            finally:
                db.close()
            return accounts, attempts
        except sqlite3.Error as err:
            last = err
            time.sleep(.5)
    raise RuntimeError(f'budget file unreadable: {last}')


def tally(attempts):
    """Paid calls and spend of a set of budget attempts, by provider kind."""
    out = {}
    for a in attempts:
        t = out.setdefault(a['kind'], dict(attempts=0, settled=0, unresolved=0, spent=0.0, inputTokens=0, outputTokens=0))
        t['attempts'] += 1
        if a['actual'] is None:
            t['unresolved'] += 1
        else:
            t['settled'] += 1
            t['spent'] = round(t['spent'] + a['actual'], 8)
        t['inputTokens'] += a['inputTokens'] or 0
        t['outputTokens'] += a['outputTokens'] or 0
    return out


class Ledger:
    """Attributes the budget file's attempts to the phase that was running when each first showed up. Attempts that existed
    before the run belong to earlier runs and are never counted here."""
    def __init__(self, path):
        self.path, self.lock = path, threading.Lock()
        self.known = set(budget_read(path)[1])
        self.owner, self.phase = {}, 'startup'

    def set_phase(self, phase):
        with self.lock:
            self.phase = phase

    def poll(self):
        accounts, attempts = budget_read(self.path)
        with self.lock:
            for i in attempts:
                if i not in self.known and i not in self.owner:
                    self.owner[i] = self.phase
        return accounts, attempts

    def by_phase(self, attempts):
        groups = collections.defaultdict(list)
        with self.lock:
            for i, phase in self.owner.items():
                if i in attempts:
                    groups[phase].append(attempts[i])
        return {phase: tally(rows) for phase, rows in groups.items()}

    def total(self, attempts):
        with self.lock:
            return tally([attempts[i] for i in self.owner if i in attempts])


# ---- the server process --------------------------------------------------------------------------------------------------------

def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))   # the local service is never reached through a proxy


def http(port, method, path, body=None, headers=None, timeout=1900):
    req = urllib.request.Request(f'http://127.0.0.1:{port}{path}', data=body, headers=headers or {}, method=method)
    try:
        with OPENER.open(req, timeout=timeout) as r:
            return r.status, r.read(), r.headers.get('Content-Type', '')
    except urllib.error.HTTPError as err:
        return err.code, err.read(), err.headers.get('Content-Type', '')


class Tail:
    """Follows a server log for the lines that say a fault happened (a degraded Search, a failed or dropped fold, JEV missing...)."""
    def __init__(self, path):
        self.path, self.pos, self.partial = path, 0, b''
        self.faults, self.lines = [], 0

    def poll(self):
        try:
            with open(self.path, 'rb') as f:
                f.seek(self.pos)
                data = f.read()
        except FileNotFoundError:
            return []
        self.pos += len(data)
        data = self.partial + data
        *complete, self.partial = data.split(b'\n')
        new = []
        for raw in complete:
            line = raw.decode('utf-8', 'replace')
            self.lines += 1
            if FAULT.search(line) and not STATUS_LINE.search(line):
                new.append(line[:400])
        self.faults += new
        return new


class Server:
    def __init__(self, label, kind, layout, env_keys, binary, dead_proxy=False):
        self.label, self.kind, self.layout = label, kind, layout
        self.dir = layout.root / label
        self.binary = pathlib.Path(binary)
        self.dead_proxy = dead_proxy
        self.port = free_port()
        self.admin_port = free_port() if kind == 'new' else None
        self.client_key = 'parity-' + secrets.token_urlsafe(24)      # throwaway, this run only
        self.admin_token = secrets.token_urlsafe(32) if kind == 'new' else None   # 43 characters, throwaway
        self.env_keys = env_keys
        self.proc = None
        self.argv = [str(self.binary), 'memory-agent', 'aml-serve', '--listen', f'127.0.0.1:{self.port}', '--data-root', str(self.dir / 'data'),
                     '--memory-config', str(layout.config), '--max-open-stores', '8']
        if kind == 'old':
            self.argv += ['--consolidate-on-add']
        else:
            self.argv += ['--consolidate', 'background', '--admin-listen', f'127.0.0.1:{self.admin_port}', '--admin-token-env', 'AML_ADMIN_TOKEN']

    def environment(self):
        env = {k: os.environ[k] for k in PASSTHROUGH if k in os.environ}
        if self.dead_proxy:
            # A miss in the model cache would call a provider: it cannot leave the machine.
            for k in PROXY_VARS + ('NO_PROXY', 'no_proxy'):
                env.pop(k, None)
            env.update({k: DEAD_PROXY for k in PROXY_VARS})
        env['MNEMON_DATA_DIR'] = str(self.dir / 'mnemon-home')
        env['AML_API_KEY'] = self.client_key
        if self.admin_token:
            env['AML_ADMIN_TOKEN'] = self.admin_token
        env.update(self.env_keys)       # the provider keys: this child's environment only
        return env

    def start(self):
        self.dir.mkdir(parents=True, exist_ok=True)
        self.log_path = self.dir / 'aml-serve.log'
        self.log = open(self.log_path, 'ab')
        self.proc = subprocess.Popen(self.argv, env=self.environment(), stdout=self.log, stderr=self.log, start_new_session=True)
        deadline = time.time() + 90
        while time.time() < deadline:
            if self.proc.poll() is not None:
                raise RuntimeError(f'aml-serve exited with {self.proc.returncode} before it was healthy; see {self.log_path}')
            try:
                if http(self.port, 'GET', '/health', timeout=2)[0] == 200:
                    break
            except OSError:
                pass
            time.sleep(.2)
        else:
            raise RuntimeError('aml-serve did not become healthy')
        if self.kind == 'new':
            while time.time() < deadline:
                try:
                    if self.admin('/ready')[0] == 200:
                        return
                except OSError:
                    pass
                time.sleep(.2)
            raise RuntimeError('the admin listener did not become ready')

    def post(self, path, body):
        return http(self.port, 'POST', path, body, {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + self.client_key})

    def admin(self, path):
        return http(self.admin_port, 'GET', path, None, {'Authorization': 'Bearer ' + self.admin_token}, timeout=30)

    def stop(self, grace=300):
        if self.proc is None:
            return dict(exitCode=None)
        info = dict(killed=False)
        if self.proc.poll() is None:
            self.proc.send_signal(signal.SIGTERM)
            try:
                self.proc.wait(grace)
            except subprocess.TimeoutExpired:
                info['killed'] = True
                self.proc.kill()
                self.proc.wait(30)
        info['exitCode'] = self.proc.returncode
        self.log.close()
        return info

    def kill(self):
        if self.proc is not None and self.proc.poll() is None:
            self.proc.kill()
            self.proc.wait(30)


# ---- one run -------------------------------------------------------------------------------------------------------------------

def load_keys():
    """The provider keys of a live run, from this process's environment. They go into the server's environment and
    nowhere else."""
    out = {}
    for name in ('DEEPSEEK_API_KEY', 'TYPESAFE_API_KEY'):
        if not os.environ.get(name):
            raise SystemExit(f'{name} is not set in the environment: a live run needs it')
        out[name] = os.environ[name]
    return out


def guard_limit(accounts):
    """The run stops itself when any account has spent and held 80% of its cap, well before the cap refuses a call."""
    return [kind for kind, a in accounts.items() if kind in CAPS and a['spent'] + a['held'] > .8 * CAPS[kind]]


def watch(server, tail, ledger, stop, state, abort_on_fault, interval=2.0):
    while not stop.wait(interval):
        try:
            faults = tail.poll()
            accounts, _ = ledger.poll()
        except Exception as err:      # a watcher failure must never take the run down silently: say it and go on
            say(stage='watch-error', error=str(err)[:200])
            continue
        over = guard_limit(accounts)
        if faults and abort_on_fault:
            state['abort'] = 'fault in the server log: ' + faults[0]
        elif over:
            state['abort'] = 'budget guard: ' + ','.join(over) + ' reached 80% of its cap'
        if state.get('abort'):
            say(stage='abort', reason=state['abort'])
            server.kill()
            return


class Probe(threading.Thread):
    """Reads the hardened server's /admin/status every few milliseconds while a user's Searches run and keeps the samples in
    which the consolidation scheduler had work (queued, running or backing off) or a Search held a user: direct evidence that a
    Search met a consolidation that was still pending."""
    def __init__(self, server, every_ms):
        super().__init__(daemon=True)
        self.server, self.every, self.quit = server, every_ms / 1000, threading.Event()
        self.total, self.samples, self.errors = 0, [], 0

    def run(self):
        start = time.perf_counter()
        while not self.quit.is_set():
            try:
                status, body, _ = self.server.admin('/admin/status')
                queue = json.loads(body)['queue']
                self.total += 1
                if any(queue[k] for k in ('queued', 'running', 'backingOff', 'held')):
                    self.samples.append(dict(atMs=round((time.perf_counter() - start) * 1000, 1), **{k: queue[k] for k in ('queued', 'running', 'backingOff', 'held')}))
            except Exception:
                self.errors += 1
            self.quit.wait(self.every)

    def result(self):
        self.quit.set()
        self.join(30)
        return dict(polls=self.total, errors=self.errors, samplesWithWork=len(self.samples), firstSamples=self.samples[:20],
                    maxHeld=max([x['held'] for x in self.samples], default=0), maxRunning=max([x['running'] for x in self.samples], default=0),
                    maxQueued=max([x['queued'] for x in self.samples], default=0))


def run_user(server, user, reqs, ledger, state, workers, out_dir, stall_ms=0, probe_ms=0):
    """One user: the Adds one after the other (the retried last Add among them), then its Searches `workers` at a time."""
    rec = {}
    (out_dir / 'responses' / user).mkdir(parents=True, exist_ok=True)

    def one(r):
        if state.get('abort'):
            return dict(seq=r['seq'], kind=r['kind'], status=-1, error='run aborted')
        start = time.perf_counter()
        try:
            status, body, ctype = server.post(r['path'], r['body'])
        except Exception as err:
            return dict(seq=r['seq'], kind=r['kind'], status=-1, error=f'{type(err).__name__}: {err}'[:300], ms=(time.perf_counter() - start) * 1000)
        ms = (time.perf_counter() - start) * 1000
        name = f"responses/{user}/{r['seq']:04d}-{r['kind']}.body"
        (out_dir / name).write_bytes(body)
        row = dict(seq=r['seq'], kind=r['kind'], status=status, ms=round(ms, 1), respBytes=len(body), respSha256=sha256(body), file=name, ctype=ctype)
        if r['kind'] == 'search':
            row.update(qid=r['qid'], inSample=r['inSample'])
            try:
                items = json.loads(body)['data']
                row.update(items=len(items), blocks=sum(1 for x in items if x['id'].startswith('index:')))
            except Exception:
                row.update(items=None, blocks=None)
        return row

    adds = [r for r in reqs if r['kind'] != 'search']
    searches = [r for r in reqs if r['kind'] == 'search']
    rows = {}
    ledger.set_phase(f'{user}:add')
    t0 = time.perf_counter()
    for r in adds:
        row = one(r)
        rows[r['seq']] = row
        ok = row['status'] == 200
        if ok:
            try:
                ok = json.loads((out_dir / row['file']).read_text()).get('success') is True
            except Exception:
                ok = False
        if not ok:
            state['abort'] = state.get('abort') or f"{user}: {r['kind']} {r['seq']} answered {row['status']} {row.get('error', '')}"
            break
        if r['seq'] % 10 == 0:
            say(stage='adds', user=user, seq=r['seq'], of=len(adds), ms=row['ms'])
    t1 = time.perf_counter()
    ledger.poll()
    if state.get('abort'):
        rec['requests'] = [rows[k] for k in sorted(rows)]
        return rec
    say(stage='adds-done', user=user, adds=len(adds), seconds=round(t1 - t0, 1))
    ledger.set_phase(f'{user}:search')
    done = [0]
    lock = threading.Lock()
    probe = Probe(server, probe_ms) if probe_ms and server.kind == 'new' else None
    resume = None
    if stall_ms and server.kind == 'new':
        # Supplementary: the server stands still from the moment the user's last Add has answered until the Searches have
        # connected, so they meet whatever consolidation was still queued or running. The requests are the same ones.
        os.kill(server.proc.pid, signal.SIGSTOP)
        resume = threading.Timer(stall_ms / 1000, lambda: os.kill(server.proc.pid, signal.SIGCONT))
        resume.start()
        rec['stallMs'] = stall_ms
    if probe:
        probe.start()

    def search(r):
        row = one(r)
        with lock:
            done[0] += 1
            if done[0] % 10 == 0 or done[0] == len(searches):
                say(stage='searches', user=user, done=done[0], of=len(searches), lastMs=row.get('ms'))
        return row

    try:
        with concurrent.futures.ThreadPoolExecutor(workers) as pool:
            for row in pool.map(search, searches):
                rows[row['seq']] = row
    finally:
        if resume:
            resume.join()
            if server.proc.poll() is None:      # a stalled server must never be left stopped
                os.kill(server.proc.pid, signal.SIGCONT)
    t2 = time.perf_counter()
    if probe:
        rec['queueProbe'] = probe.result()
    ledger.poll()
    rec['requests'] = [rows[k] for k in sorted(rows)]
    rec['seconds'] = dict(adds=round(t1 - t0, 3), searches=round(t2 - t1, 3), total=round(t2 - t0, 3))
    return rec


def cmd_run(args, keys=None, dead_proxy=False):
    """One build over the whole request sequence. keys are the provider keys for the server's environment (a live
    run's, read from the environment unless dry); `replay` gives placeholders and a dead proxy."""
    layout = Layout(args.dry)
    label = args.label or args.which
    kind = args.which
    if not pathlib.Path(args.binary).is_file():
        raise SystemExit(f'{args.binary}: no such binary')
    if kind == 'new' and label == 'old':
        raise SystemExit("a run of the new build must not take the label 'old', the baseline's directory")
    out_dir = layout.root / label
    if out_dir.exists():
        if not args.force:
            raise SystemExit(f'{out_dir} exists; pass --force to start this run again from an empty data root')
        shutil.rmtree(out_dir)
    config = write_config(layout)
    users = args.users or USERS
    plan = {u: build_requests(u, args.sessions, args.searches) for u in users}
    if keys is None:
        keys = {} if args.dry else load_keys()
    ledger = Ledger(layout.budget)
    server = Server(label, kind, layout, keys, args.binary, dead_proxy)
    state = dict()
    stop = threading.Event()
    manifest = dict(label=label, kind=kind, dry=args.dry, binary=str(server.binary), binarySha256=sha256_file(server.binary),
                    builtFrom=args.built_from, deadProxy=dead_proxy,
                    argv=[a for a in server.argv], envNames=sorted(server.environment()), startedAt=datetime.datetime.now(datetime.timezone.utc).isoformat(),
                    searchWorkers=args.workers, stallMs=args.stall_ms, probeMs=args.probe_ms, users={})
    started = time.perf_counter()
    say(stage='start', label=label, kind=kind, port=server.port, adminPort=server.admin_port, users=users,
        requests={u: dict(adds=sum(r['kind'] != 'search' for r in p), searches=sum(r['kind'] == 'search' for r in p)) for u, p in plan.items()})
    tail = Tail(server.dir / 'aml-serve.log')
    watcher = threading.Thread(target=watch, args=(server, tail, ledger, stop, state, kind == 'old' and not args.dry), daemon=True)
    try:
        server.start()
        watcher.start()
        for user in users:
            rec = run_user(server, user, plan[user], ledger, state, args.workers, out_dir, args.stall_ms, args.probe_ms)
            for row, r in zip(rec['requests'], [r for r in plan[user] if r['seq'] in {x['seq'] for x in rec['requests']}]):
                row['reqSha256'], row['reqBytes'] = r['reqSha256'], len(r['body'])
            if server.kind == 'new' and not state.get('abort'):
                status, body, _ = server.admin('/admin/status')
                rec['adminStatus'] = json.loads(body) if status == 200 else dict(status=status)
            manifest['users'][user] = rec
            dump(out_dir / 'manifest.json', manifest)
            if state.get('abort'):
                break
        if server.kind == 'new' and not state.get('abort'):
            status, body, _ = server.admin('/metrics')
            (out_dir / 'metrics.txt').write_bytes(b'\n'.join(l for l in body.splitlines() if l.startswith(b'mnemon_aml_') and b'_bucket{' not in l) + b'\n')
    except BaseException as err:
        state['abort'] = state.get('abort') or f'{type(err).__name__}: {err}'[:300]
        raise
    finally:
        stop.set()
        if watcher.is_alive():
            watcher.join(10)
        if state.get('abort'):
            server.kill()
        manifest['shutdown'] = server.stop()
        tail.poll()
        manifest['wallSeconds'] = round(time.perf_counter() - started, 3)
        manifest['abort'] = state.get('abort')
        ledger.set_phase('shutdown')
        accounts, attempts = ledger.poll()
        manifest['budget'] = dict(accounts=accounts, run=ledger.total(attempts), byPhase=ledger.by_phase(attempts), attemptsInFile=len(attempts))
        manifest['log'] = dict(lines=tail.lines, faults=tail.faults[:50], faultCount=len(tail.faults))
        manifest['finishedAt'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        dump(out_dir / 'manifest.json', manifest)
    say(stage='finished', label=label, abort=manifest['abort'], exit=manifest['shutdown'], faults=manifest['log']['faultCount'],
        paid=manifest['budget']['run'], wallSeconds=manifest['wallSeconds'])
    return 1 if manifest['abort'] else 0


# ---- the comparison ------------------------------------------------------------------------------------------------------------

def first_difference(a, b, context=70):
    n = min(len(a), len(b))
    i = next((k for k in range(n) if a[k] != b[k]), n)
    show = lambda x: x[max(0, i - context):i + context].decode('utf-8', 'replace')
    return dict(firstDifferingByte=i, oldLength=len(a), newLength=len(b), oldExcerpt=show(a), newExcerpt=show(b))


def item_difference(a, b):
    """A Search body's difference in terms of the AML items: which ids, in what order, which field."""
    try:
        x, y = json.loads(a)['data'], json.loads(b)['data']
    except Exception as err:
        return dict(note=f'bodies are not both Search responses: {err}')
    out = dict(oldItems=len(x), newItems=len(y), oldBlocks=sum(i['id'].startswith('index:') for i in x), newBlocks=sum(i['id'].startswith('index:') for i in y))
    ids_x, ids_y = [i['id'] for i in x], [i['id'] for i in y]
    out['sameIdSet'], out['sameIdOrder'] = set(ids_x) == set(ids_y), ids_x == ids_y
    for k, (p, q) in enumerate(zip(x, y)):
        if p != q:
            out.update(firstDifferingItem=k, fields=[f for f in p if p.get(f) != q.get(f)], oldId=p['id'], newId=q['id'])
            break
    return out


def overlay_dump(root, user):
    """The consolidated state of one user's store: every table of memory-index.db, read from a copy, with the model cache's
    hit counter ("cached") taken out of the JSON payloads; and the number of model calls the cache answered."""
    key = hashlib.sha256(user.encode()).hexdigest()[:32]
    src = root / 'data' / 'users' / key
    if not (src / 'memory-index.db').exists():
        return None
    with tempfile.TemporaryDirectory() as tmp:
        for f in src.glob('memory-index.db*'):
            shutil.copy2(f, tmp)
        db = sqlite3.connect(str(pathlib.Path(tmp) / 'memory-index.db'))
        cached = [0]

        def scrub(v):
            if isinstance(v, dict):
                out = {}
                for k, x in v.items():
                    if k == 'cached' and isinstance(x, int):
                        cached[0] += x      # the model cache's hit counter: counted, and left out of the comparison
                    else:
                        out[k] = scrub(x)
                return out
            if isinstance(v, list):
                return [scrub(x) for x in v]
            return v

        def norm(cell):
            if isinstance(cell, str) and cell[:1] in '{[':
                try:
                    return json.dumps(scrub(json.loads(cell)), sort_keys=True, ensure_ascii=False)
                except ValueError:
                    return cell
            if isinstance(cell, bytes):
                return 'blob:' + sha256(cell)
            return cell

        tables = {}
        for (name,) in db.execute("select name from sqlite_master where type='table' and name not like 'sqlite_%' order by name").fetchall():
            # a `token` column is the digest of the payload next to it, which holds the cache's counter: left out with it
            columns = [c[1] for c in db.execute(f'pragma table_info("{name}")') if c[1] != 'token']
            if not columns:
                continue
            rows = [tuple(norm(c) for c in row) for row in db.execute('select ' + ','.join(f'"{c}"' for c in columns) + f' from "{name}"')]
            tables[name] = dict(columns=columns, rows=sorted(rows, key=lambda r: json.dumps(r, default=str)))
        db.close()
    return dict(tables=tables, cachedCalls=cached[0])


def state_summary(dump_):
    """Counters from the timelines payload that count model calls and batches."""
    table = (dump_ or {}).get('tables', {}).get('timelines')
    if not table:
        return None
    out = {}
    for row in table['rows']:
        cell = dict(zip(table['columns'], row))
        try:
            payload = json.loads(cell['payload'])
        except Exception:
            continue
        key = cell.get('scope') or 'default'
        out[key] = {k: payload.get(k) for k in ('calls', 'batches', 'failed', 'watermark')}
        out[key]['pending'] = len(payload.get('pending') or [])
        out[key]['memory'] = {k: len(v) for k, v in (payload.get('memory') or {}).items() if hasattr(v, '__len__')}
    return out


def compare_runs(layout, old_label, new_label):
    """Compares two runs' manifests and saved bodies; returns the report."""
    mo = json.loads((layout.root / old_label / 'manifest.json').read_text())
    mn = json.loads((layout.root / new_label / 'manifest.json').read_text())
    report = dict(task='parity replay of aml-serve (--consolidate background) against a recorded --consolidate-on-add run', dry=layout.dry,
                  generatedAt=datetime.datetime.now(datetime.timezone.utc).isoformat(), users={}, differences=[])
    complete = not mo.get('abort') and not mn.get('abort')
    all_identical = complete
    for user in sorted(set(mo['users']) | set(mn['users'])):
        uo, un = mo['users'].get(user), mn['users'].get(user)
        entry = dict(adds=dict(sent=0, identical=0), addRetry=dict(sent=0, identical=0), searches=dict(sent=0, identical=0, inDefaultSample=0, identicalInDefaultSample=0),
                     differences=[], sameRequests=True)
        if not uo or not un or len(uo['requests']) != len(un['requests']):
            entry['incomplete'] = dict(old=len(uo['requests']) if uo else None, new=len(un['requests']) if un else None)
            all_identical = False
        else:
            for a, b in zip(uo['requests'], un['requests']):
                if a['reqSha256'] != b['reqSha256'] or a['kind'] != b['kind']:
                    entry['sameRequests'] = False
                    all_identical = False
                slot = {'add': 'adds', 'add-retry': 'addRetry', 'search': 'searches'}[a['kind']]
                entry[slot]['sent'] += 1
                if a['kind'] == 'search' and a.get('inSample'):
                    entry[slot]['inDefaultSample'] += 1
                body_a = (layout.root / old_label / a['file']).read_bytes() if 'file' in a else None
                body_b = (layout.root / new_label / b['file']).read_bytes() if 'file' in b else None
                same = body_a is not None and body_a == body_b and a['status'] == b['status']
                if same:
                    entry[slot]['identical'] += 1
                    if a['kind'] == 'search' and a.get('inSample'):
                        entry[slot]['identicalInDefaultSample'] += 1
                    continue
                all_identical = False
                diff = dict(user=user, seq=a['seq'], kind=a['kind'], qid=a.get('qid'), oldStatus=a['status'], newStatus=b['status'])
                if body_a is not None and body_b is not None:
                    diff.update(first_difference(body_a, body_b))
                    if a['kind'] == 'search':
                        diff['items'] = item_difference(body_a, body_b)
                entry['differences'].append(diff)
        report['differences'] += entry['differences'][:5]
        entry['conversationRequests'] = dict(adds=entry['adds']['sent'] + entry['addRetry']['sent'], searches=entry['searches']['sent'])
        for tag, m, u in (('old', mo, uo), ('new', mn, un)):
            if not u:
                continue
            phases = {p.split(':', 1)[1]: v for p, v in m['budget']['byPhase'].items() if p.startswith(user + ':')}
            kinds = collections.defaultdict(lambda: dict(attempts=0, settled=0, unresolved=0, spent=0.0, inputTokens=0, outputTokens=0))
            for v in phases.values():
                for kind, t in v.items():
                    for k2, x in t.items():
                        kinds[kind][k2] = round(kinds[kind][k2] + x, 8)
            entry[tag] = dict(paidCalls=sum(t['attempts'] for t in kinds.values()), paidByProvider=dict(kinds), byStage=phases,
                              wallSeconds=u.get('seconds'), searchLatencyMs=latency([r for r in u['requests'] if r['kind'] == 'search']),
                              searchesWithoutLayerBlocks=sum(1 for r in u['requests'] if r['kind'] == 'search' and r.get('blocks') == 0))
            if tag == 'new':
                entry['new']['adminStatusAfterUser'] = u.get('adminStatus')
        # the consolidated state of the stores: identical but for the cache's hit counter
        do, dn = overlay_dump(layout.root / old_label, user), overlay_dump(layout.root / new_label, user)
        if do and dn:
            same_state = do['tables'] == dn['tables']
            entry['consolidatedState'] = dict(identicalExceptCacheCounter=same_state, oldCachedCalls=do['cachedCalls'], newCachedCalls=dn['cachedCalls'],
                                              old=state_summary(do), new=state_summary(dn), tables=sorted(do['tables']))
            if not same_state:
                entry['consolidatedState']['differingTables'] = [t for t in do['tables'] if do['tables'].get(t) != dn['tables'].get(t)]
        report['users'][user] = entry
    paid_new = mn['budget']['run']
    report['builds'] = dict(old=dict(label=old_label, binary=mo['binary'], sha256=mo['binarySha256'], argv=mo['argv'], exit=mo.get('shutdown'),
                                     wallSeconds=mo.get('wallSeconds'), logFaults=mo['log']['faultCount'], logFaultSamples=mo['log']['faults'][:5]),
                            new=dict(label=new_label, binary=mn['binary'], sha256=mn['binarySha256'], argv=mn['argv'], exit=mn.get('shutdown'),
                                     wallSeconds=mn.get('wallSeconds'), logFaults=mn['log']['faultCount'], logFaultSamples=mn['log']['faults'][:5]))
    report['paid'] = dict(old=mo['budget']['run'], new=paid_new, accountsAfterOld=mo['budget']['accounts'], accountsAfterNew=mn['budget']['accounts'])
    new_paid_calls = sum(t['attempts'] for t in paid_new.values())
    report['newRunPaidCalls'] = new_paid_calls
    faults = mo['log']['faultCount'] + mn['log']['faultCount']
    report['verdict'] = 'PASS' if all_identical and complete and new_paid_calls == 0 and faults == 0 else ('INCOMPLETE' if not complete else 'FAIL')
    return report


def run_summary(report, layout, label):
    """What a supplementary run adds to the report: its counts, its paid calls and what the stall and the probe saw."""
    manifest = json.loads((layout.root / label / 'manifest.json').read_text())
    users = {}
    for user, e in report['users'].items():
        rec = manifest['users'].get(user, {})
        users[user] = dict(searches=e['searches'], adds=e['adds'], addRetry=e['addRetry'], differences=e['differences'][:3],
                           stallMs=rec.get('stallMs'), queueProbe=rec.get('queueProbe'),
                           consolidatedStateIdentical=(e.get('consolidatedState') or {}).get('identicalExceptCacheCounter'),
                           newCachedCalls=(e.get('consolidatedState') or {}).get('newCachedCalls'))
    return dict(verdict=report['verdict'], newRunPaidCalls=report['newRunPaidCalls'], logFaults=report['builds']['new']['logFaults'],
                wallSeconds=manifest.get('wallSeconds'), stallMs=manifest.get('stallMs'), probeMs=manifest.get('probeMs'), users=users)


def cache_entries(layout):
    """Rows of the shared model cache (read from a copy: the file is in WAL mode)."""
    if not layout.cache.exists():
        return None
    with tempfile.TemporaryDirectory() as tmp:
        for f in layout.cache.parent.glob(layout.cache.name + '*'):
            shutil.copy2(f, tmp)
        db = sqlite3.connect(str(pathlib.Path(tmp) / layout.cache.name))
        rows = db.execute('select count(*), coalesce(sum(length(body)), 0) from replies').fetchone()
        db.close()
    return dict(replies=rows[0], bytes=rows[1])


def task_facts(layout, report, old_label, new_label):
    """The facts about the whole task: builds, configuration, datasets, the shared cache and the budget file."""
    shipped = json.loads(CONFIG.read_text()) if CONFIG.exists() else {}
    effective = json.loads(layout.config.read_text())
    mo = json.loads((layout.root / old_label / 'manifest.json').read_text())
    mn = json.loads((layout.root / new_label / 'manifest.json').read_text())
    cache = cache_entries(layout)
    old_paid = sum(t['attempts'] for t in mo['budget']['run'].values())
    accounts = budget_read(layout.budget)[0]
    new_stores = {u: e.get('consolidatedState') for u, e in report['users'].items()}
    return dict(
        commits=dict(old=mo.get('builtFrom'), new=mn.get('builtFrom')),
        configuration=dict(source='deploy/aml/memory-config.industrial.json', sourceSha256=sha256_file(CONFIG) if CONFIG.exists() else None,
                           bm25Only='embedding' not in effective, changedFromSource=sorted(k for k in effective if effective.get(k) != shipped.get(k)),
                           effective=effective, effectiveSha256=sha256_file(layout.config)),
        datasets={f: sha256_file(INPUTS / f) if INPUTS and (INPUTS / f).is_file() else None for f in DATASET_FILES},
        environment=dict(python=sys.version.split()[0], platform=sys.platform, searchWorkers=SEARCH_WORKERS, topK=TOP_K),
        modelCache=dict(shared=str(layout.cache), entriesAfterAllRuns=cache, oldPaidCalls=old_paid,
                        entriesEqualOldPaidCalls=bool(cache) and cache['replies'] == old_paid),
        budget=dict(file=str(layout.budget), caps=CAPS, accountsNow=accounts),
        consolidationCallsAnsweredByTheCache={u: (st or {}).get('newCachedCalls') for u, st in new_stores.items()},
    )


def cmd_compare(args):
    layout = Layout(args.dry)
    report = compare_runs(layout, args.old, args.new)
    report.update(task_facts(layout, report, args.old, args.new))
    if args.also:
        report['supplementary'] = {label: run_summary(compare_runs(layout, args.old, label), layout, label) for label in args.also}
    dump(layout.root / 'report.json', report)
    say(stage='compare', verdict=report['verdict'], users={u: dict(searches=e['searches'], adds=e['adds'], addRetry=e['addRetry']) for u, e in report['users'].items()},
        newPaidCalls=report['newRunPaidCalls'], faults=report['builds']['old']['logFaults'] + report['builds']['new']['logFaults'],
        supplementary={k: dict(verdict=v['verdict'], paid=v['newRunPaidCalls'], searchesIdentical={u: x['searches']['identical'] for u, x in v['users'].items()}) for k, v in report.get('supplementary', {}).items()})
    return 0 if report['verdict'] == 'PASS' and all(v['verdict'] == 'PASS' for v in report.get('supplementary', {}).values()) else 1


def cmd_replay(args):
    """The new build over the whole request sequence from the model cache only, compared with the recorded old run. The
    server is given two things a live run is not:
      - placeholder provider keys: the real ones are never read, so the server could not authenticate to a provider even if
        it tried;
      - every proxy variable pointing at a dead loopback port and NO_PROXY cleared, so that a cache miss cannot leave the
        machine: it would fail unsent, show as a budget attempt (released) and as a degraded Search, and the comparison
        would fail.
    The result is ROOT/LABEL/compare-report.json; ROOT/report.json (the comparison of a live pair) is left alone. Exit 0
    when the verdict is PASS: every body identical, the same requests, 0 paid calls, no fault in the log."""
    layout = Layout(args.dry)
    if args.label == args.old:
        raise SystemExit(f'the replay must not replace the run it compares with: {args.label!r} is the old run; choose another label')
    if not (layout.root / args.old / 'manifest.json').is_file():
        raise SystemExit(f'{layout.root / args.old / "manifest.json"} is missing: record the old run first (run old), or point --old at it')
    cache_before = cache_entries(layout)
    attempts_before = len(budget_read(layout.budget)[1])
    run = argparse.Namespace(dry=args.dry, which='new', label=args.label, force=args.force, users=args.users, workers=args.workers,
                             stall_ms=0, probe_ms=0, sessions=args.sessions, searches=args.searches, binary=args.binary, built_from=args.built_from)
    code = cmd_run(run, keys=dict(PLACEHOLDER_KEYS), dead_proxy=True)
    report = compare_runs(layout, args.old, args.label)
    report['replay'] = dict(
        binary=args.binary, binarySha256=sha256_file(args.binary), builtFrom=args.built_from, cacheBefore=cache_before,
        cacheAfter=cache_entries(layout), budgetAttemptsBefore=attempts_before, budgetAttemptsAfter=len(budget_read(layout.budget)[1]),
        realKeysRead=False, proxies=DEAD_PROXY + ' (dead)', runExit=code)
    out = layout.root / args.label / 'compare-report.json'
    dump(out, report)
    users = {u: dict(searches=e['searches'], adds=e['adds'], addRetry=e['addRetry'], sameRequests=e['sameRequests'],
                     differences=len(e['differences']), state=(e.get('consolidatedState') or {}).get('identicalExceptCacheCounter'))
             for u, e in report['users'].items()}
    print(json.dumps(dict(verdict=report['verdict'], newRunPaidCalls=report['newRunPaidCalls'], users=users,
                          logFaults=report['builds']['new']['logFaults'], replay=report['replay'], report=str(out)), indent=1))
    return 0 if report['verdict'] == 'PASS' else 1


def latency(rows):
    ms = sorted(r['ms'] for r in rows if r.get('ms') is not None)
    if not ms:
        return None
    return dict(p50=ms[len(ms) // 2], p90=ms[int(len(ms) * .9)], max=ms[-1])


# ---- smoke test and plan --------------------------------------------------------------------------------------------------------

def cmd_smoke(args):
    """Start each binary on the configuration with throwaway paths and make no request: the flags, the configuration and the
    admin listener are accepted. Spends nothing."""
    layout = Layout(True)
    layout.make_private()
    c = effective_config(layout)
    c['planner'] = c['judge'] = c['consolidator'] = dict(kind='local', model='dummy', baseURL='http://127.0.0.1:9/v1', timeoutSeconds=5)
    layout.config.write_text(json.dumps(c, indent=2) + '\n')
    for kind, binary in (('old', args.old_binary), ('new', args.new_binary)):
        server = Server('smoke-' + kind, kind, layout, {}, binary)
        try:
            server.start()
            extra = {}
            if kind == 'new':
                extra = dict(ready=server.admin('/ready')[0], status=json.loads(server.admin('/admin/status')[1]).get('version'))
            say(stage='smoke', kind=kind, healthy=True, **extra)
        finally:
            say(stage='smoke-stop', kind=kind, **server.stop(60))
    return 0


def cmd_scrub(args):
    """Looks for the provider keys in every file under the task directory (but the build output) and says which files hold one.
    It prints file names only, never a value."""
    layout = Layout(args.dry)
    needles = [v.encode() for v in load_keys().values()]
    keep = max(map(len, needles)) - 1
    skipped = [layout.root / 'bin', layout.root / 'src']
    hits, files = [], 0
    for path in sorted(layout.root.rglob('*')):
        if not path.is_file() or any(path.is_relative_to(s) for s in skipped):
            continue
        files += 1
        tail = b''
        with open(path, 'rb') as f:
            for chunk in iter(lambda: f.read(8 << 20), b''):
                window = tail + chunk
                if any(n in window for n in needles):
                    hits.append(str(path.relative_to(layout.root)))
                    break
                tail = window[-keep:]
    say(stage='scrub', filesScanned=files, filesWithAKey=hits)
    return 1 if hits else 0


def cmd_plan(args):
    for user in args.users or USERS:
        reqs = build_requests(user, args.sessions, args.searches)
        adds = [r for r in reqs if r['kind'] == 'add']
        searches = [r for r in reqs if r['kind'] == 'search']
        say(user=user, addRequests=len(adds), retries=sum(r['kind'] == 'add-retry' for r in reqs), searches=len(searches),
            searchesInDefaultSample=sum(r['inSample'] for r in searches), addBytes=sum(len(r['body']) for r in adds),
            planSha256=sha256(b''.join(r['body'] for r in reqs)))
    say(datasets={f: sha256_file(INPUTS / f) for f in DATASET_FILES})
    return 0


def main():
    global ROOT, INPUTS, CONFIG
    ap = argparse.ArgumentParser(description=__doc__.split('\n')[0], epilog='See README.md beside this file.')
    ap.add_argument('--root', required=True, help='the task directory: the runs (ROOT/old, ROOT/<label>), the model cache and the budget file (ROOT/private) and the configuration')
    ap.add_argument('--inputs', help='the directory of the six dataset files (README.md): needed to plan, run and replay')
    ap.add_argument('--config', help='the memory configuration both builds run on (BM25 only); default: deploy/aml/memory-config.industrial.json of this repository')
    ap.add_argument('--dry', action='store_true', help='local models that fail at once, a private output directory (ROOT/dry), no keys: a plumbing test that spends nothing')
    sub = ap.add_subparsers(dest='stage', required=True)

    def sequence(p):
        p.add_argument('--users', nargs='*', choices=USERS); p.add_argument('--workers', type=int, default=SEARCH_WORKERS)
        p.add_argument('--sessions', type=int, help='debug: only the first N sessions of a conversation'); p.add_argument('--searches', type=int, help='debug: only the first N questions')
        p.add_argument('--force', action='store_true', help='start the run again from an empty data root, replacing its directory')
        p.add_argument('--binary', required=True, help='the aml-serve build to run (mnemon)')
        p.add_argument('--built-from', help='what the build was made from (a commit), recorded in the manifest')

    p = sub.add_parser('run', help='one build over the whole sequence, with the live providers (keys in the environment)')
    p.add_argument('which', choices=['old', 'new']); p.add_argument('--label', help='the run\'s directory name under ROOT (default: old or new)')
    sequence(p)
    p.add_argument('--stall-ms', type=int, default=0, help='supplementary (new only): stop the server with SIGSTOP while each user\'s Searches connect, for this long')
    p.add_argument('--probe-ms', type=int, default=0, help='supplementary (new only): poll /admin/status this often during the Searches and keep the samples with work in the queue')
    p = sub.add_parser('replay', help='the new build from the model cache only (placeholder keys, dead proxies), compared with the recorded old run')
    p.add_argument('label', help='the run\'s directory name under ROOT'); p.add_argument('--old', default='old', help='the recorded old run to compare with')
    sequence(p)
    p = sub.add_parser('compare', help='two runs: bodies, consolidated state, paid calls'); p.add_argument('--old', default='old'); p.add_argument('--new', default='new')
    p.add_argument('--also', nargs='*', help='supplementary runs (labels) to compare with the old run too')
    p = sub.add_parser('smoke', help='start each build on throwaway paths and make no request')
    p.add_argument('--old-binary', required=True); p.add_argument('--new-binary', required=True)
    sub.add_parser('scrub', help='look for the provider keys (from the environment) in every file under ROOT')
    p = sub.add_parser('plan', help='the requests of each user, counted and hashed'); p.add_argument('--users', nargs='*', choices=USERS); p.add_argument('--sessions', type=int); p.add_argument('--searches', type=int)
    args = ap.parse_args()
    ROOT = pathlib.Path(args.root).resolve()
    if args.inputs:
        INPUTS = pathlib.Path(args.inputs).resolve()
    if args.config:
        CONFIG = pathlib.Path(args.config).resolve()
    if INPUTS is None and args.stage in ('plan', 'run', 'replay'):
        ap.error(f'{args.stage} needs --inputs')
    return dict(run=cmd_run, replay=cmd_replay, compare=cmd_compare, smoke=cmd_smoke, scrub=cmd_scrub, plan=cmd_plan)[args.stage](args)


if __name__ == '__main__':
    sys.exit(main())
