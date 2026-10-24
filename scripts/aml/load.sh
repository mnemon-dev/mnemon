#!/bin/bash
# scripts/aml/load.sh: the load test of aml-serve: the latency and the
# errors of Adds, Searches or both at rising rates, and the knee of each.
#
#   scripts/aml/load.sh --base-url URL --key-file FILE --mode add|search|mixed
#       --rates R,R,… [--duration SECONDS] [--users N] [--messages N]
#       [--prepare N] [--admin-url URL --admin-token-file FILE]
#       [--timeout SECONDS] [--settle SECONDS] [--min-baseline N]
#       [--keep-going] [--validate] [--out DIR]
#   scripts/aml/load.sh --local [--binary PATH [--built-from REV]]
#       [--deepseek-latency D] [--jev-latency D] [--port-base PORT]
#       [--limits "ADDS SEARCHES OPEN_STORES WORKERS"]
#       --mode … --rates … [the options above but the URLs and files]
#
# Each step sends requests at a constant rate R (per second; decimals are
# fine) with vegeta v12.13.0 (go run github.com/tsenart/vegeta/v12@v12.13.0,
# or the binary $VEGETA names) and waits for the answers. Adds are new
# sessions of --messages messages (default 20) with distinct request ids,
# spread over --users users (default 16); Searches ask those users the
# questions of lib.sh with top_k 100; mixed sends both, each at R. The users
# are new every run (load-<run>-u<k>): --prepare N first adds N sessions to
# each (default 6 for search and mixed, 0 for add), waits for consolidation,
# and searches each user once, so that the steps fold no tail. Delete them
# afterwards on a shared instance.
#
# The method of the knee, per route:
#   - A step lasts --duration seconds: at least 300 for search and mixed
#     (their default), 60 by default for add.
#   - Before every step, the first included, and after the preparation, the
#     script waits up to --settle seconds (default 900; 0 for none) for the
#     consolidation scheduler to have no work, so that a step does not
#     inherit the backlog of the one before; each step records whether it
#     started settled. It needs the admin listener; without it there is no
#     Add knee. An Add step that did not start settled is void, and so is
#     every step after it: the Add knee comes from the steps before it.
#   - The lowest rate is the baseline, which needs --min-baseline answered
#     requests (default 50).
#   - The knee is the highest rate whose p95 stays within twice the
#     baseline's p95, and whose p50 within twice its p50, with no non-2xx
#     answer, every lower rate holding too. A sweep stops after the first
#     step that misses it, unless --keep-going.
#   - --validate checks the tooling with shorter steps and reports no knee.
#
# The API key is read from --key-file (mode 0600, not readable by others; a
# line AML_API_KEY=… of an instance's env file is read as the key) and
# travels to vegeta in its targets on a pipe and to curl as a config on its
# standard input, never as an argument. --base-url must hold no user:password@.
# With --admin-url and --admin-token-file (the same rules for the token, and
# a line MNEMON_ADMIN_TOKEN=…) the script also reads /metrics and
# /admin/status: the peak of requests in flight, the shed, degraded and
# partial Searches and the outran Adds of each step, and the consolidation
# queue it settles on. A request that takes longer than --timeout seconds
# (default 300) counts as an error (code 0).
#
# --local builds the fake provider and, without --binary, mnemon from the
# tree these scripts are in (--binary runs that mnemon, whose commit
# --built-from records), runs them on loopback (the fake answering DeepSeek
# in --deepseek-latency, default 2s, and JEV in --jev-latency, default 1.5s;
# aml-serve with --limits, default the dedicated "16 16 16 4", background
# consolidation and the shipped configuration aimed at the fake), and uses
# throwaway secrets.
#
# It writes DIR/report.json: per step and route the requests, the status
# codes, the non-2xx count, p50/p95/p99/max in seconds, the throughput and
# the admin numbers; per route the knee, or why there is none; the build it
# measured. Exit status, the script's own: 0 when the run completed (its
# knee is a measurement, not a verdict), 1 when a secret reached DIR, 2 when
# the run could not be made (a usage error, a tool or a server that failed,
# an interruption).
#
# Test hook: AML_LOAD_FAIL_AT=prepare makes the script fail (exit 2) while
# the preparation's Adds are in flight, and AML_LOAD_FAIL_AT=step while the
# first step's attacks and sampler run, so that a test can check the
# cleanup leaves no process.
set -euo pipefail
umask 077
export LC_ALL=C

readonly PROG=aml-load
# shellcheck source=scripts/aml/lib.sh
. "$(dirname "$0")/lib.sh"
aml_trap_exit

BASE_URL= KEY_ARG= MODE= RATES= DURATION= USERS=16 MESSAGES=20 PREPARE= TIMEOUT=300 SETTLE=900 MIN_BASELINE=50
KEEP_GOING=0 VALIDATE=0 OUT= AML_ADMIN_URL= TOKEN_ARG= LOCAL=0 BINARY= BUILT_FROM= DS_LATENCY=2s JEV_LATENCY=1.5s
LIMITS="16 16 16 4" PORT_BASE=18720
# The shortest Search step the knee accepts.
readonly SEARCH_STEP=300

usage() {
	sed -n '5,13p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
}
while [ $# -gt 0 ]; do
	case $1 in
	--local | --keep-going | --validate)
		case $1 in --local) LOCAL=1 ;; --keep-going) KEEP_GOING=1 ;; --validate) VALIDATE=1 ;; esac
		shift
		continue
		;;
	esac
	# Every other option takes a value: one without it is a usage error (2),
	# never an unbound $2 under set -u.
	[ $# -ge 2 ] || usage
	case $1 in
	--base-url) BASE_URL=${2%/} ;;
	--key-file) KEY_ARG=$2 ;;
	--mode) MODE=$2 ;;
	--rates) RATES=$2 ;;
	--duration) DURATION=$2 ;;
	--users) USERS=$2 ;;
	--messages) MESSAGES=$2 ;;
	--prepare) PREPARE=$2 ;;
	--admin-url) AML_ADMIN_URL=${2%/} ;;
	--admin-token-file) TOKEN_ARG=$2 ;;
	--timeout) TIMEOUT=$2 ;;
	--settle) SETTLE=$2 ;;
	--min-baseline) MIN_BASELINE=$2 ;;
	--out) OUT=$2 ;;
	--binary) BINARY=$2 ;;
	--built-from) BUILT_FROM=$2 ;;
	--deepseek-latency) DS_LATENCY=$2 ;;
	--jev-latency) JEV_LATENCY=$2 ;;
	--limits) LIMITS=$2 ;;
	--port-base) PORT_BASE=$2 ;;
	*) usage ;;
	esac
	shift 2
done

whole='^[1-9][0-9]*$'
case $MODE in add | search | mixed) ;; *) aml_die "--mode must be add, search or mixed" ;; esac
[ -n "$DURATION" ] || DURATION=$([ "$MODE" = add ] && echo 60 || echo "$SEARCH_STEP")
[[ $DURATION =~ $whole ]] || aml_die "--duration must be whole seconds"
if [ "$MODE" != add ] && [ "$DURATION" -lt "$SEARCH_STEP" ] && [ "$VALIDATE" = 0 ]; then
	aml_die "a Search step lasts at least ${SEARCH_STEP}s for the knee (--validate runs shorter ones, with no knee)"
fi
[[ $USERS =~ $whole ]] || aml_die "--users must be a whole number"
[[ $MESSAGES =~ $whole ]] || aml_die "--messages must be a whole number"
[[ $TIMEOUT =~ $whole ]] || aml_die "--timeout must be whole seconds"
[[ $SETTLE =~ ^[0-9]+$ ]] || aml_die "--settle must be whole seconds"
[[ $MIN_BASELINE =~ ^[0-9]+$ ]] || aml_die "--min-baseline must be a whole number"
[ -n "$PREPARE" ] || PREPARE=$([ "$MODE" = add ] && echo 0 || echo 6)
[[ $PREPARE =~ ^[0-9]+$ ]] || aml_die "--prepare must be a whole number"
[ -n "$RATES" ] || aml_die "--rates is required"
RATE_LIST=$(tr ',' ' ' <<<"$RATES")
previous=0
for rate in $RATE_LIST; do
	# vegeta paces whole requests a minute; the knee needs rising rates.
	awk -v r="$rate" -v p="$previous" 'BEGIN { exit !(r + 0 > p && r * 60 >= 1 && r == r + 0) }' ||
		aml_die "rates must rise, each a number of at least 1/60: $RATES"
	previous=$rate
done
[ -z "$BUILT_FROM" ] || [ -n "$BINARY" ] || aml_die "--built-from goes with --binary"
[ -z "$BINARY" ] || [ "$LOCAL" = 1 ] || aml_die "--binary goes with --local"
aml_need curl jq awk sort od
[ -n "${VEGETA:-}" ] || aml_need go

# private FILE NAME: FILE is a regular file no one else may read.
private() {
	local mode
	[ -f "$1" ] && [ -r "$1" ] || aml_die "$2 $1 is not a readable file"
	mode=$(stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1")
	case $mode in *00) ;; *) aml_die "$2 $1 is readable by others (mode $mode): make it 0600" ;; esac
}

WORK=$(mktemp -d "${TMPDIR:-/tmp}/aml-load.XXXXXX")
KEY_FILE=$WORK/api.key AML_ADMIN_TOKEN_FILE=
# The background jobs the cleanup kills, with what they started, before
# anything is removed: the preparation's requests, a step's attacks and its
# sampler.
AML_FAKE_PID= AML_SERVE_PID= PREPARERS= SAMPLER= ATTACKERS=
cleanup() {
	local status=$? pid
	for pid in $PREPARERS $ATTACKERS $SAMPLER; do
		aml_kill_tree TERM "$pid"
	done
	for pid in $PREPARERS $ATTACKERS $SAMPLER; do
		wait "$pid" 2>/dev/null || true
	done
	aml_stop "$AML_SERVE_PID" "$AML_FAKE_PID"
	rm -rf "$WORK"
	exit "$(aml_exit_status "$status")"
}
trap cleanup EXIT
trap 'exit 2' INT TERM

if [ "$LOCAL" = 1 ]; then
	[ -z "$BASE_URL$KEY_ARG$AML_ADMIN_URL$TOKEN_ARG" ] || aml_die "--local makes its own URLs and secrets"
	aml_need go pgrep
	if [ -n "$BINARY" ]; then
		[ -x "$BINARY" ] || aml_die "--binary $BINARY is not an executable file"
		BINARY=$(cd "$(dirname "$BINARY")" && pwd)/$(basename "$BINARY")
	fi
	fake=127.0.0.1:$PORT_BASE api=127.0.0.1:$((PORT_BASE + 1)) admin=127.0.0.1:$((PORT_BASE + 2))
	for port in $PORT_BASE $((PORT_BASE + 1)) $((PORT_BASE + 2)); do
		aml_port_free "$port" || aml_die "127.0.0.1:$port is in use; pick another --port-base"
	done
	[ -n "$OUT" ] || OUT=$AML_REPO/.testdata/aml/load-$MODE-$(date -u +%Y%m%dT%H%M%SZ)
	mkdir -p "$OUT"
	aml_secret "$WORK/api.key"
	aml_secret "$WORK/admin.token"
	aml_secret "$WORK/provider.key"
	aml_build_fake "$WORK"
	if [ -z "$BINARY" ]; then
		aml_say "building mnemon from $AML_REPO"
		aml_build_mnemon "$WORK"
		BINARY=$WORK/mnemon
		BUILD=$(aml_provenance | jq -c '. + {binary: "built from the scripts tree"}')
	else
		BUILD=$(jq -cn --arg rev "$BUILT_FROM" '{binary: "given (--binary)", commit: (if $rev == "" then null else $rev end), dirty: (if $rev == "" then null else false end)}')
	fi
	aml_start_fake "$WORK/fakeprovider" "$fake" "$DS_LATENCY" "$JEV_LATENCY" "$OUT/fakeprovider.log"
	aml_config "$WORK/memory-config.json" "http://$fake/chat/completions" "http://$fake/v1/systemone" "$WORK/model-budget.db"
	aml_start_serve "$BINARY" "$WORK" "$WORK/memory-config.json" "$api" "$admin" "$LIMITS" "$OUT/aml-serve.log"
	BASE_URL=http://$api AML_ADMIN_URL=http://$admin AML_ADMIN_TOKEN_FILE=$WORK/admin.token
else
	[[ $BASE_URL =~ ^https?://[^/]+ ]] || aml_die "--base-url must be an http or https URL"
	[[ ! $BASE_URL =~ ^https?://[^/]*@ ]] || aml_die "--base-url must hold no user or password (user:password@): the key goes in --key-file"
	[ -n "$KEY_ARG" ] || aml_die "--key-file is required"
	private "$KEY_ARG" "--key-file"
	aml_private_copy "$KEY_ARG" AML_API_KEY "$KEY_FILE"
	if [ -n "$AML_ADMIN_URL$TOKEN_ARG" ]; then
		[ -n "$AML_ADMIN_URL" ] && [ -n "$TOKEN_ARG" ] || aml_die "--admin-url and --admin-token-file go together"
		[[ ! $AML_ADMIN_URL =~ ^https?://[^/]*@ ]] || aml_die "--admin-url must hold no user or password"
		private "$TOKEN_ARG" "--admin-token-file"
		AML_ADMIN_TOKEN_FILE=$WORK/admin.token
		aml_private_copy "$TOKEN_ARG" MNEMON_ADMIN_TOKEN "$AML_ADMIN_TOKEN_FILE"
	fi
	BUILD=null
	[ -n "$OUT" ] || OUT=$AML_REPO/.testdata/aml/load-$MODE-$(date -u +%Y%m%dT%H%M%SZ)
	mkdir -p "$OUT"
fi
OUT=$(cd "$OUT" && pwd)
ADMIN=$([ -n "$AML_ADMIN_URL" ] && echo 1 || echo 0)
SCRIPTS=$(aml_provenance)
curl -fsS -o /dev/null --max-time 10 "$BASE_URL/health" || aml_die "$BASE_URL/health does not answer"

vegeta() {
	if [ -n "${VEGETA:-}" ]; then
		"$VEGETA" "$@"
	else
		go run "$AML_VEGETA_MODULE" "$@"
	fi
}

RUN=$(date -u +%Y%m%d%H%M%S)$(od -An -N2 -tx1 /dev/urandom | tr -d ' \n')
USER_IDS=
k=1
while [ "$k" -le "$USERS" ]; do
	USER_IDS="$USER_IDS load-$RUN-u$k"
	k=$((k + 1))
done
SESSION=1
STEP=0

# post ROUTE BODY: one request with the key, for the preparation; prints the
# status code.
post() {
	aml_bearer "$KEY_FILE" | curl --config - -sS -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
		--data-binary "@$2" --max-time "$TIMEOUT" "$BASE_URL/$1" 2>/dev/null || true
}

# settle LIMIT: waits up to LIMIT seconds for the consolidation scheduler to
# have no work (admin only). SETTLED says whether it had none in time: true,
# false, or null when nothing was checked (no admin access, or --settle 0).
SETTLED=null
settle() {
	local deadline=$(($(date +%s) + $1))
	SETTLED=null
	[ "$ADMIN" = 1 ] && [ "$1" -gt 0 ] || return 0
	until aml_admin /admin/status | jq -e '.queue | .queued + .running + .backingOff == 0' >/dev/null; do
		if [ "$(date +%s)" -ge "$deadline" ]; then
			aml_say "the consolidation queue still has work after ${1}s"
			SETTLED=false
			return 0
		fi
		sleep 2
	done
	SETTLED=true
}

# targets ROUTE N [BODIES]: N vegeta targets of ROUTE in its http format,
# the key in their header; an Add's body is BODIES-<i>.json, a Search's one
# of $WORK/searches, in turn.
targets() {
	local key i=0 body
	IFS= read -r key <"$KEY_FILE"
	while [ "$i" -lt "$2" ]; do
		i=$((i + 1))
		if [ "$1" = search ]; then
			body=$WORK/searches/$(((i - 1) % SEARCH_BODIES + 1)).json
		else
			body=$3-$i.json
		fi
		printf 'POST %s/%s\nAuthorization: Bearer %s\nContent-Type: application/json\n@%s\n\n' "$BASE_URL" "$1" "$key" "$body"
	done
}

# The Search bodies: every user with every question, in an order that
# changes user each time.
mkdir -p "$WORK/searches"
SEARCH_BODIES=0
q=0
while [ "$q" -lt 12 ]; do
	for user in $USER_IDS; do
		SEARCH_BODIES=$((SEARCH_BODIES + 1))
		aml_search_body "$user" "$((q + SEARCH_BODIES))" >"$WORK/searches/$SEARCH_BODIES.json"
	done
	q=$((q + 1))
done

if [ "$PREPARE" -gt 0 ]; then
	aml_say "preparing $USERS users with $PREPARE sessions each"
	aml_add_bodies "$WORK/prepare" "prepare-$RUN" "$USER_IDS" $((USERS * PREPARE)) "$MESSAGES" "$SESSION"
	SESSION=$((SESSION + USERS * PREPARE))
	# Each user's sessions in order, the users at once. (A bare wait would
	# wait for the local servers too.)
	k=0 pids=
	for user in $USER_IDS; do
		k=$((k + 1))
		(
			i=$k
			while [ "$i" -le $((USERS * PREPARE)) ]; do
				code=$(post add "$WORK/prepare/prepare-$RUN-$i.json")
				[ "$code" = 200 ] || echo "$code" >>"$WORK/prepare.failed"
				i=$((i + USERS))
			done
		) &
		pids="$pids $!"
		PREPARERS=$pids
	done
	[ "${AML_LOAD_FAIL_AT:-}" != prepare ] || aml_die "failing on purpose while the preparation's Adds run (AML_LOAD_FAIL_AT=prepare)"
	# shellcheck disable=SC2086
	wait $pids
	PREPARERS=
	[ ! -e "$WORK/prepare.failed" ] || aml_die "preparing Adds answered $(sort -u "$WORK/prepare.failed" | tr '\n' ' ')"
	settle "$SETTLE"
	k=0 pids=
	for user in $USER_IDS; do
		k=$((k + 1))
		post search "$WORK/searches/$k.json" >/dev/null &
		pids="$pids $!"
		PREPARERS=$pids
	done
	# shellcheck disable=SC2086
	wait $pids
	PREPARERS=
	settle "$SETTLE"
fi

# sample FILE: /metrics every 2 s, the requests in flight by route, until
# killed or the work directory is gone.
sample() {
	while [ -d "$WORK" ]; do
		aml_admin /metrics 2>/dev/null | awk '/^mnemon_aml_in_flight[{]/ { print }' >>"$1" 2>/dev/null || true
		sleep 2
	done
}

# counters: the admin numbers a step changes, as JSON.
counters() {
	local shed
	[ "$ADMIN" = 1 ] || {
		echo null
		return
	}
	shed=$(aml_admin /metrics | awk '/^mnemon_aml_shed_total[{]/ { r = $1; sub(/.*route="/, "", r); sub(/".*/, "", r); s[r] = $2 }
		END { printf "{\"add\":%d,\"search\":%d}", s["add"], s["search"] }')
	aml_admin /admin/status | jq -c --argjson shed "$shed" '{degraded, partial, addOutran, searches, adds, shed: $shed}'
}

# attack ROUTE RATE DIR: one vegeta attack; writes DIR/ROUTE.json, vegeta's
# report. An Add attack's bodies are DIR/bodies/load-<run>-<step>-<i>.json.
attack() {
	local per_minute n
	per_minute=$(awk -v r="$2" 'BEGIN { printf "%d", r * 60 + 0.5 }')
	n=$(requests "$2")
	targets "$1" "$n" "$3/bodies/load-$RUN-$STEP" | vegeta attack -format=http -rate="$per_minute/1m" -duration="${DURATION}s" \
		-timeout="${TIMEOUT}s" -max-body=0 >"$3/$1.bin"
	vegeta report -type=json "$3/$1.bin" >"$3/$1.json"
	rm -f "$3/$1.bin"
}

# requests RATE: the targets a step at RATE needs, and a few to spare.
requests() { awk -v r="$1" -v d="$DURATION" 'BEGIN { printf "%d", r * d + 5 }'; }

STARTED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
: >"$OUT/steps.jsonl"
routes=$MODE
if [ "$MODE" = mixed ]; then routes="add search"; fi
stopped=
for rate in $RATE_LIST; do
	STEP=$((STEP + 1))
	dir=$WORK/step-$STEP
	mkdir -p "$dir"
	# Every step, the first too, starts from a scheduler with no work, or
	# says it did not: STEP_SETTLED is recorded with the step.
	settle "$SETTLE"
	STEP_SETTLED=$SETTLED
	aml_say "step $STEP: $MODE at $rate/s for ${DURATION}s"
	before=$(counters)
	if [ "$ADMIN" = 1 ]; then
		: >"$dir/inflight.txt"
		sample "$dir/inflight.txt" &
		SAMPLER=$!
	fi
	ATTACKERS=
	for route in $routes; do
		if [ "$route" = add ]; then
			aml_add_bodies "$dir/bodies" "load-$RUN-$STEP" "$USER_IDS" "$(requests "$rate")" "$MESSAGES" "$SESSION"
			SESSION=$((SESSION + $(requests "$rate")))
		fi
		attack "$route" "$rate" "$dir" &
		ATTACKERS="$ATTACKERS $!"
	done
	[ "${AML_LOAD_FAIL_AT:-}" != step ] || aml_die "failing on purpose while step $STEP runs (AML_LOAD_FAIL_AT=step)"
	# shellcheck disable=SC2086
	wait $ATTACKERS || aml_die "step $STEP: an attack failed"
	ATTACKERS=
	if [ -n "$SAMPLER" ]; then
		aml_kill_tree TERM "$SAMPLER"
		wait "$SAMPLER" 2>/dev/null || true
		SAMPLER=
	fi
	after=$(counters)
	for route in $routes; do
		[ -s "$dir/$route.json" ] || aml_die "step $STEP: vegeta wrote no report for $route"
		peak=null
		if [ "$ADMIN" = 1 ]; then
			peak=$(awk -v route="$route" 'index($0, "route=\"" route "\"") { if ($2 > m) m = $2 } END { printf "%d", m }' "$dir/inflight.txt")
		fi
		jq -c --arg route "$route" --argjson rate "$rate" --argjson duration "$DURATION" --argjson step "$STEP" \
			--argjson before "$before" --argjson after "$after" --argjson peak "$peak" --argjson settled "$STEP_SETTLED" '
			def s: . / 1e9;
			{step: $step, route: $route, rate: $rate, durationSeconds: $duration, settled: $settled, requests,
			 codes: .status_codes, non2xx: ([.status_codes | to_entries[] | select(.key | test("^2") | not) | .value] | add // 0),
			 p50: (.latencies["50th"] | s), p95: (.latencies["95th"] | s), p99: (.latencies["99th"] | s), max: (.latencies.max | s),
			 mean: (.latencies.mean | s), success, throughput, peakInFlight: $peak,
			 errors: (.errors // [] | .[:5]),
			 admin: (if $before == null then null else {
				shed: ($after.shed[$route] - $before.shed[$route]),
				degraded: ($after.degraded - $before.degraded), partial: ($after.partial - $before.partial),
				addOutran: ($after.addOutran - $before.addOutran)} end)}' "$dir/$route.json" >>"$OUT/steps.jsonl"
	done
	tail -n "$(wc -w <<<"$routes")" "$OUT/steps.jsonl" | jq -r '"  \(.route) \(.rate)/s: \(.requests) requests, p50 \(.p50 * 1000 | round / 1000) s, p95 \(.p95 * 1000 | round / 1000) s, p99 \(.p99 * 1000 | round / 1000) s, non-2xx \(.non2xx), peak in flight \(.peakInFlight)"' >&2
	# The knee so far: the sweep stops after the first step that misses it.
	missed=$(jq -s -L "$AML_REPO/scripts/aml" --arg routes "$routes" 'include "knee";
		[($routes | split(" "))[] as $r | [.[] | select(.route == $r)] | .[0] as $base | .[-1] | select(missed($base))] | length' "$OUT/steps.jsonl")
	if [ "$missed" -gt 0 ] && [ "$KEEP_GOING" = 0 ]; then
		stopped=$rate
		aml_say "step $STEP missed the knee criterion; the sweep stops here (--keep-going runs on)"
		break
	fi
done

jq -s -L "$AML_REPO/scripts/aml" --arg started "$STARTED" --arg finished "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg mode "$MODE" --arg rates "$RATES" \
	--arg target "$BASE_URL" --argjson local "$LOCAL" --arg dsLatency "$DS_LATENCY" --arg jevLatency "$JEV_LATENCY" --arg limits "$LIMITS" \
	--argjson duration "$DURATION" --argjson users "$USERS" --argjson messages "$MESSAGES" --argjson prepare "$PREPARE" \
	--argjson timeout "$TIMEOUT" --arg stopped "$stopped" --arg run "$RUN" --argjson build "$BUILD" --argjson scripts "$SCRIPTS" \
	--argjson validate "$VALIDATE" --argjson settle "$SETTLE" --argjson minBaseline "$MIN_BASELINE" \
	--argjson searchStep "$SEARCH_STEP" 'include "knee";
	. as $steps |
	{kind: "aml-load", started: $started, finished: $finished, mode: $mode, rates: $rates, target: $target,
	 build: $build, scripts: $scripts, validation: ($validate == 1),
	 local: (if $local == 1 then {fakeLatency: {deepseek: $dsLatency, jev: $jevLatency}, limits: $limits,
		limitsAre: "--max-concurrent-adds --max-concurrent-searches --max-open-stores --consolidation-workers",
		configuration: "deploy/aml/memory-config.industrial.json; only testEndpoint (the fake provider) and budgetFile changed"} else null end),
	 durationSeconds: $duration, users: $users, userPrefix: "load-\($run)-u", messagesPerAdd: $messages, preparedSessionsPerUser: $prepare,
	 requestTimeoutSeconds: $timeout, settleSeconds: $settle, settledEveryStep: ([$steps[].settled] | all(. == true)), minBaselineRequests: $minBaseline,
	 tool: "vegeta v12.13.0", stoppedAfterRate: (if $stopped == "" then null else ($stopped | tonumber) end),
	 kneeRule: "the highest rate whose p95 stays within twice the baseline (lowest rate) p95 and whose p50 within twice its p50, with no non-2xx answer, every lower rate holding too; Search steps of \($searchStep) s at least, the scheduler settled before each step (an Add step that did not start settled, and every one after it, is void), at least \($minBaseline) answered requests at the baseline",
	 steps: $steps,
	 knee: ([$steps[].route] | unique | map(. as $r | {($r): ([$steps[] | select(.route == $r)] | knee($r; $validate == 1; $minBaseline))}) | add)}' \
	"$OUT/steps.jsonl" >"$OUT/report.json"
rm -f "$OUT/steps.jsonl"

# The script's own check that no secret reached its output.
for secret in "$KEY_FILE" ${AML_ADMIN_TOKEN_FILE:+"$AML_ADMIN_TOKEN_FILE"}; do
	if grep -rqF -f "$secret" "$OUT"; then
		aml_say "FAIL: a secret appears in $OUT"
		AML_EXIT_OURS=1
		exit 1
	fi
done
jq -r '.knee | to_entries[] | "knee \(.key): \(if .value.knee == null then "none: \(.value.reason)" else "\(.value.knee.rate)/s, p50 \(.value.knee.p50 * 1000 | round / 1000) s, p95 \(.value.knee.p95 * 1000 | round / 1000) s, peak in flight \(.value.knee.peakInFlight)\(if .value.void then " (\(.value.void))" else "" end)" end)"' "$OUT/report.json" >&2
aml_say "report: $OUT/report.json"
