#!/bin/bash
# scripts/aml/load.sh: the load test of aml-serve (hardening plan, Task 12
# Step 3): latency and errors of Adds, Searches or both at rising rates.
#
#   scripts/aml/load.sh --base-url URL --key-file FILE --mode add|search|mixed
#       --rates R,R,… [--duration SECONDS] [--users N] [--messages N]
#       [--prepare N] [--admin-url URL --admin-token-file FILE]
#       [--timeout SECONDS] [--settle SECONDS] [--keep-going] [--out DIR]
#   scripts/aml/load.sh --local [--deepseek-latency D] [--jev-latency D]
#       [--limits "ADDS SEARCHES OPEN_STORES WORKERS"] [--port-base PORT]
#       --mode … --rates … [options above but the URLs and files]
#
# Each step sends requests at a constant rate R (per second; decimals are
# fine) for --duration seconds (default 60) with vegeta v12.13.0 (go run
# github.com/tsenart/vegeta/v12@v12.13.0, or the binary $VEGETA names) and
# waits for the answers. Adds are new sessions of --messages messages
# (default 20) with distinct request ids, spread over --users users (default
# 16); Searches ask those users the questions of lib.sh with top_k 100; mixed
# sends both, each at R. The users are new for every run (load-<run>-u<k>):
# --prepare N first adds N sessions to each (default 6 for search and mixed,
# 0 for add), waits for consolidation, and searches each user once so that
# the steps do not fold a tail. Delete them afterwards on a shared instance.
#
# The API key is read from --key-file (mode 0600, not readable by others)
# and travels to vegeta in its targets on a pipe and to curl as a config on
# its standard input; it is never an argument. With --admin-url and
# --admin-token-file (the same rules for the token) the script also reads
# /metrics and /admin/status: the peak of requests in flight, the shed,
# degraded and partial Searches and the outran Adds of each step, and the
# consolidation queue it waits on (--settle SECONDS between steps, default
# 0; the preparation waits up to 15 minutes).
#
# It writes DIR/report.json: per step and route, the requests, the status
# codes, the non-2xx count, p50/p95/p99/max in seconds, the success ratio,
# the throughput and the admin numbers; and per route the knee: the highest
# rate whose p95 stays within twice the lowest rate's p95 with no non-2xx
# answer, every lower rate holding too. A sweep stops after the first step
# that misses it, unless --keep-going. A request that takes longer than
# --timeout (default 300) counts as an error (code 0). --local builds mnemon
# and the fake provider, runs them on loopback (the fake answering DeepSeek
# in --deepseek-latency, default 2s, and JEV in --jev-latency, default 1.5s;
# aml-serve with --limits, default the dedicated "16 16 16 4", background
# consolidation and the shipped configuration aimed at the fake), and uses
# throwaway secrets.
set -euo pipefail
umask 077
export LC_ALL=C

readonly PROG=aml-load
# shellcheck source=scripts/aml/lib.sh
. "$(dirname "$0")/lib.sh"

BASE_URL= KEY_FILE= MODE= RATES= DURATION=60 USERS=16 MESSAGES=20 PREPARE= TIMEOUT=300 SETTLE=0 KEEP_GOING=0 OUT=
AML_ADMIN_URL= AML_ADMIN_TOKEN_FILE= LOCAL=0 DS_LATENCY=2s JEV_LATENCY=1.5s LIMITS="16 16 16 4" PORT_BASE=18720

usage() {
	sed -n '5,11p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
}
while [ $# -gt 0 ]; do
	case $1 in
	--local) LOCAL=1 && shift && continue ;;
	--keep-going) KEEP_GOING=1 && shift && continue ;;
	--base-url) BASE_URL=${2%/} ;;
	--key-file) KEY_FILE=$2 ;;
	--mode) MODE=$2 ;;
	--rates) RATES=$2 ;;
	--duration) DURATION=$2 ;;
	--users) USERS=$2 ;;
	--messages) MESSAGES=$2 ;;
	--prepare) PREPARE=$2 ;;
	--admin-url) AML_ADMIN_URL=${2%/} ;;
	--admin-token-file) AML_ADMIN_TOKEN_FILE=$2 ;;
	--timeout) TIMEOUT=$2 ;;
	--settle) SETTLE=$2 ;;
	--out) OUT=$2 ;;
	--deepseek-latency) DS_LATENCY=$2 ;;
	--jev-latency) JEV_LATENCY=$2 ;;
	--limits) LIMITS=$2 ;;
	--port-base) PORT_BASE=$2 ;;
	*) usage ;;
	esac
	shift 2 || usage
done

whole='^[1-9][0-9]*$'
case $MODE in add | search | mixed) ;; *) aml_die "--mode must be add, search or mixed" ;; esac
[[ $DURATION =~ $whole ]] || aml_die "--duration must be whole seconds"
[[ $USERS =~ $whole ]] || aml_die "--users must be a whole number"
[[ $MESSAGES =~ $whole ]] || aml_die "--messages must be a whole number"
[[ $TIMEOUT =~ $whole ]] || aml_die "--timeout must be whole seconds"
[[ $SETTLE =~ ^[0-9]+$ ]] || aml_die "--settle must be whole seconds"
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
aml_need curl jq awk sort
[ -n "${VEGETA:-}" ] || aml_need go

# private FILE NAME: FILE is a regular file no one else may read.
private() {
	local mode
	[ -f "$1" ] && [ -r "$1" ] || aml_die "$2 $1 is not a readable file"
	mode=$(stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1")
	case $mode in *00) ;; *) aml_die "$2 $1 is readable by others (mode $mode): make it 0600" ;; esac
}

WORK=$(mktemp -d "${TMPDIR:-/tmp}/aml-load.XXXXXX")
AML_FAKE_PID= AML_SERVE_PID= SAMPLER=
cleanup() {
	[ -z "$SAMPLER" ] || kill "$SAMPLER" 2>/dev/null || true
	aml_stop "$AML_SERVE_PID" "$AML_FAKE_PID"
	rm -rf "$WORK"
}
trap cleanup EXIT
trap 'exit 2' INT TERM

if [ "$LOCAL" = 1 ]; then
	[ -z "$BASE_URL$KEY_FILE$AML_ADMIN_URL$AML_ADMIN_TOKEN_FILE" ] || aml_die "--local makes its own URLs and secrets"
	aml_need go od pkill
	fake=127.0.0.1:$PORT_BASE api=127.0.0.1:$((PORT_BASE + 1)) admin=127.0.0.1:$((PORT_BASE + 2))
	for port in $PORT_BASE $((PORT_BASE + 1)) $((PORT_BASE + 2)); do
		aml_port_free "$port" || aml_die "127.0.0.1:$port is in use; pick another --port-base"
	done
	[ -n "$OUT" ] || OUT=$AML_REPO/.testdata/aml/load-$MODE-$(date -u +%Y%m%dT%H%M%SZ)
	mkdir -p "$OUT"
	aml_secret "$WORK/api.key"
	aml_secret "$WORK/admin.token"
	aml_secret "$WORK/provider.key"
	aml_say "building mnemon and the fake provider"
	aml_build "$WORK"
	aml_start_fake "$WORK/fakeprovider" "$fake" "$DS_LATENCY" "$JEV_LATENCY" "$OUT/fakeprovider.log"
	aml_config "$WORK/memory-config.json" "http://$fake/chat/completions" "http://$fake/v1/systemone" "$WORK/model-budget.db"
	aml_start_serve "$WORK/mnemon" "$WORK" "$WORK/memory-config.json" "$api" "$admin" "$LIMITS" "$OUT/aml-serve.log"
	BASE_URL=http://$api KEY_FILE=$WORK/api.key AML_ADMIN_URL=http://$admin AML_ADMIN_TOKEN_FILE=$WORK/admin.token
else
	[[ $BASE_URL =~ ^https?://[^/]+ ]] || aml_die "--base-url must be an http or https URL"
	[ -n "$KEY_FILE" ] || aml_die "--key-file is required"
	private "$KEY_FILE" "--key-file"
	if [ -n "$AML_ADMIN_URL$AML_ADMIN_TOKEN_FILE" ]; then
		[ -n "$AML_ADMIN_URL" ] && [ -n "$AML_ADMIN_TOKEN_FILE" ] || aml_die "--admin-url and --admin-token-file go together"
		private "$AML_ADMIN_TOKEN_FILE" "--admin-token-file"
	fi
	[ -n "$OUT" ] || OUT=$AML_REPO/.testdata/aml/load-$MODE-$(date -u +%Y%m%dT%H%M%SZ)
	mkdir -p "$OUT"
fi
OUT=$(cd "$OUT" && pwd)
ADMIN=$([ -n "$AML_ADMIN_URL" ] && echo 1 || echo 0)
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
# have no work (admin only).
settle() {
	local deadline=$(($(date +%s) + $1))
	[ "$ADMIN" = 1 ] || return 0
	until aml_admin /admin/status | jq -e '.queue | .queued + .running + .backingOff == 0' >/dev/null; do
		[ "$(date +%s)" -lt "$deadline" ] || {
			aml_say "the consolidation queue still has work after ${1}s"
			return 0
		}
		sleep 2
	done
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
	aml_add_bodies "$WORK/prepare" prepare-$RUN "$USER_IDS" $((USERS * PREPARE)) "$MESSAGES" "$SESSION"
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
	done
	# shellcheck disable=SC2086
	wait $pids
	[ ! -e "$WORK/prepare.failed" ] || aml_die "preparing Adds answered $(sort -u "$WORK/prepare.failed" | tr '\n' ' ')"
	settle 900
	k=0 pids=
	for user in $USER_IDS; do
		k=$((k + 1))
		post search "$WORK/searches/$k.json" >/dev/null &
		pids="$pids $!"
	done
	# shellcheck disable=SC2086
	wait $pids
	settle 900
fi

# sample FILE: /metrics every 2 s, the requests in flight by route, until
# killed.
sample() {
	while :; do
		aml_admin /metrics 2>/dev/null | awk '/^mnemon_aml_in_flight\{/ { print }' >>"$1" || true
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
[ "$MODE" = mixed ] && routes="add search"
stopped=
for rate in $RATE_LIST; do
	STEP=$((STEP + 1))
	dir=$WORK/step-$STEP
	mkdir -p "$dir"
	aml_say "step $STEP: $MODE at $rate/s for ${DURATION}s"
	before=$(counters)
	if [ "$ADMIN" = 1 ]; then
		: >"$dir/inflight.txt"
		sample "$dir/inflight.txt" &
		SAMPLER=$!
	fi
	attackers=
	for route in $routes; do
		if [ "$route" = add ]; then
			aml_add_bodies "$dir/bodies" "load-$RUN-$STEP" "$USER_IDS" "$(requests "$rate")" "$MESSAGES" "$SESSION"
			SESSION=$((SESSION + $(requests "$rate")))
		fi
		attack "$route" "$rate" "$dir" &
		attackers="$attackers $!"
	done
	# shellcheck disable=SC2086
	wait $attackers || aml_die "step $STEP: an attack failed"
	if [ -n "$SAMPLER" ]; then
		kill "$SAMPLER" 2>/dev/null || true
		wait "$SAMPLER" 2>/dev/null || true
		SAMPLER=
	fi
	after=$(counters)
	for route in $routes; do
		[ -s "$dir/$route.json" ] || aml_die "step $STEP: vegeta wrote no report for $route"
		peak=$( [ "$ADMIN" = 1 ] && awk -v route="$route" 'index($0, "route=\"" route "\"") { if ($2 > m) m = $2 } END { printf "%d", m }' "$dir/inflight.txt" || echo null)
		jq -c --arg route "$route" --argjson rate "$rate" --argjson duration "$DURATION" --argjson step "$STEP" \
			--argjson before "$before" --argjson after "$after" --argjson peak "${peak:-null}" '
			def s: . / 1e9;
			{step: $step, route: $route, rate: $rate, durationSeconds: $duration, requests,
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
	# The knee so far: stop after the first step that misses it.
	missed=$(jq -s --arg routes "$routes" '
		[($routes | split(" "))[] as $r | [.[] | select(.route == $r)] | (.[0].p95) as $base | .[-1] | select(.p95 > 2 * $base or .non2xx > 0)] | length' "$OUT/steps.jsonl")
	if [ "$missed" -gt 0 ] && [ "$KEEP_GOING" = 0 ]; then
		stopped=$rate
		aml_say "step $STEP missed the knee criterion; the sweep stops here (--keep-going runs on)"
		break
	fi
	[ "$SETTLE" = 0 ] || settle "$SETTLE"
done

jq -s --arg started "$STARTED" --arg finished "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg mode "$MODE" --arg rates "$RATES" \
	--arg target "$BASE_URL" --argjson local "$LOCAL" --arg dsLatency "$DS_LATENCY" --arg jevLatency "$JEV_LATENCY" --arg limits "$LIMITS" \
	--argjson duration "$DURATION" --argjson users "$USERS" --argjson messages "$MESSAGES" --argjson prepare "$PREPARE" \
	--argjson timeout "$TIMEOUT" --arg stopped "$stopped" --arg run "$RUN" \
	--arg commit "$(git -C "$AML_REPO" rev-parse --short HEAD 2>/dev/null || echo unknown)" '
	. as $steps |
	{kind: "aml-load", started: $started, finished: $finished, commit: $commit, mode: $mode, rates: $rates, target: $target,
	 local: (if $local == 1 then {fakeLatency: {deepseek: $dsLatency, jev: $jevLatency}, limits: $limits,
		limitsAre: "--max-concurrent-adds --max-concurrent-searches --max-open-stores --consolidation-workers",
		configuration: "deploy/aml/memory-config.industrial.json; only testEndpoint (the fake provider) and budgetFile changed"} else null end),
	 durationSeconds: $duration, users: $users, userPrefix: "load-\($run)-u", messagesPerAdd: $messages, preparedSessionsPerUser: $prepare,
	 requestTimeoutSeconds: $timeout, tool: "vegeta v12.13.0", stoppedAfterRate: (if $stopped == "" then null else ($stopped | tonumber) end),
	 kneeRule: "the highest rate whose p95 stays within twice the lowest rate p95 with no non-2xx answer, every lower rate holding too",
	 steps: $steps,
	 knee: ([$steps[].route] | unique | map(. as $r | [$steps[] | select(.route == $r)] as $s | ($s[0].p95) as $base |
		{(.): (reduce $s[] as $x ({ok: true, knee: null}; if .ok and $x.p95 <= 2 * $base and $x.non2xx == 0 then .knee = $x else .ok = false end)
			| .knee | if . == null then null else {rate, p50, p95, p99, peakInFlight, baselineP95: $base} end)}) | add)}' \
	"$OUT/steps.jsonl" >"$OUT/report.json"
rm -f "$OUT/steps.jsonl"

# The script's own check that no secret reached its output.
for secret in "$KEY_FILE" ${AML_ADMIN_TOKEN_FILE:+"$AML_ADMIN_TOKEN_FILE"}; do
	if grep -rqF -f "$secret" "$OUT"; then
		aml_say "FAIL: a secret appears in $OUT"
		exit 1
	fi
done
jq -r '.knee | to_entries[] | "knee \(.key): \(if .value == null then "none (the lowest rate already missed it)" else "\(.value.rate)/s, p50 \(.value.p50 * 1000 | round / 1000) s, p95 \(.value.p95 * 1000 | round / 1000) s, peak in flight \(.value.peakInFlight)" end)"' "$OUT/report.json" >&2
aml_say "report: $OUT/report.json"
