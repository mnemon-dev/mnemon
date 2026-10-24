#!/bin/bash
# scripts/aml/drill.sh: the fault drill of aml-serve, on one machine and with
# no real provider: each scenario of the table below against a steady
# workload, judged by numbers both ways (the fault took effect, and the
# service held).
#
#   scripts/aml/drill.sh [--out DIR] [--only NAME,…] [--repeat N]
#       [--binary PATH [--built-from REV]] [--search-rate R] [--add-rate R]
#       [--users N] [--seed-adds N] [--error-status CODE] [--port-base PORT]
#
# It builds the fake provider (scripts/aml/fakeprovider) and, without
# --binary, mnemon from the tree these scripts are in; with --binary it runs
# that mnemon, whose commit --built-from records. It starts the fake provider
# on loopback (DeepSeek replies in 2 s, JEV in 1.5 s), toxiproxy v2.12.0 in
# front of it when a scenario needs it (go run …/cmd/server@v2.12.0, driven
# through its HTTP API with curl), and aml-serve: --consolidate background,
# the dedicated limits (16 Adds, 16 Searches, 16 open stores, 4 workers), an
# admin listener on loopback with a token (--admin-token-env), and the
# shipped configuration with only its models' testEndpoint and its budget
# file changed. It seeds the users (--seed-adds Adds each, default 10), then
# runs each scenario, --repeat times (default 1), against a steady workload:
# a Search every 1/R s (--search-rate, default 0.25) and an Add of 10
# messages every 1/R s (--add-rate, default 0.5), spread over --users users
# (default 4). A scenario: 10 s of workload, the fault for its window, 15 s
# more (and on, up to 180 s, until every breaker has closed again), the end
# of the requests in flight, the drain of the consolidation backlog, and the
# end of the model requests still in flight. It reads /metrics and
# /admin/status with the admin token (curl --config -, never an argument),
# samples /admin/status every second, and writes DIR/report.json (the
# matrix, each scenario's detail, the build it ran), DIR/matrix.md and the
# logs. A run with --only is a partial run, and says so.
#
# Exit status, the drill's own: 0 when every hard criterion held, 1 when one
# did not (or a secret reached DIR), 2 when the drill could not run (a usage
# error, a tool or a server that failed, an interruption).
#
# Test hooks: AML_DRILL_FAIL_AT=fault makes the drill fail (exit 2) right
# after it applied the first scenario's fault, while the workload and the
# sampler run, so that a test can check the cleanup leaves no process.
# AML_DRILL_SCENARIOS=FILE runs the table in FILE (the columns below) in
# place of this one; AML_DRILL_RESIDUAL_LIMIT=N lowers the residual limit.
#
# The scenario table: name|class|window|fault type|its specification|
# searches|effect|degraded|partial|p95|breaker|retries|what it is.
#   - class: hard and expected scenarios fail the run on any criterion;
#     measured ones fail it only on the validity criteria (below) and report
#     the rest.
#   - searches: the fewest Searches the scenario must answer, at 0.25 a
#     second (scaled by --search-rate).
#   - effect: what shows the fault took effect, which must be above 0:
#     retries (retries, failed and breaker-open attempts), throttled,
#     invalid, breaker-open (attempts of that outcome), rejected:KIND
#     (/admin/status rejectedByKind), fake:KIND:OUTCOME (what the fake
#     provider counted; KIND all for both). none: no fault, and none of
#     retries, failed, breaker-open, throttled, invalid or rejected may show.
#   - degraded, partial: 0 (none may), >0 (some must), - (not judged).
#   - p95: the most seconds the Search p95 may take, or -.
#   - breaker: cycle (a breaker opens during the fault and every one has
#     closed again within 180 s of its end), steady (none opens), or -.
#   - retries: 0:KIND (the calls of that kind are not retried), or -.
# Every scenario also has: non2xx (no non-2xx answer but deliberate 503
# sheds), drain (the scheduler has no work within 600 s of the fault's end),
# residual (its largest backlog, then, below 10,000 characters), held (every
# account's held amount back to 0 within 90 s after the drain) and withoutJEV
# (no "without JEV" line, no link fallback). The validity criteria, which
# fail any run: searches, effect, non2xx, held and withoutJEV.
#
# How the scenarios are sized, from internal/memoryagent/resilience.go and
# the shipped configuration (no timeoutSeconds but the consolidator's 600):
#   - A Search's calls (Interactive: the planner, DeepSeek, and the judge,
#     JEV) make 4 attempts of 45 s (DeepSeek) or 20 s (JEV); retries wait a
#     random part of 0.5, 1 and 2 s, or a Retry-After up to 10 s.
#   - A Search's flush (Flush) makes 3 attempts of at most 180 s; background
#     jobs (Background) 3 of the hard timeout; retries after up to 2 and 4 s,
#     or a Retry-After up to 5 min.
#   - Every attempt fails once its reply has sent nothing for 60 s.
#   - A 429 without Retry-After is retried patiently by Interactive and Flush
#     calls, apart from their attempts: after at most 2, 4, 8 and 16 s, 30 s
#     in all a call. A 401, 402 or 403 (rejected) is not retried.
#   - A provider's breaker opens after 10 availability failures in a row (a
#     429, an invalid reply and a rejection are none), refuses calls for
#     20 s, and closes after 2 probes answer.
# So a Search's call outlasts a hang or a reply past its attempt timeout for
# 3 attempt timeouts (60 s JEV, 135 s DeepSeek), a 429 storm with a
# Retry-After R ≤ 10 s for 3R, and one without for up to 30 s; a stall costs
# at most 60 s an attempt. The hard scenarios are sized under that: a 30 s
# blackhole, 12 s 429 storms, a 20 s one without Retry-After, replies 50 s
# late for 40 s, 30% of connections reset, malformed replies of both kinds
# at 5% (one invalid reply is retried), and stalls of 5% (their Search p95
# under the 180 s Flush attempt cap). The expected ones go past it:
# connections refused for 120 s, and an empty balance (402) of either kind.
set -euo pipefail
umask 077
export LC_ALL=C

readonly PROG=aml-drill
# shellcheck source=scripts/aml/lib.sh
. "$(dirname "$0")/lib.sh"

OUT= ONLY= REPEAT=1 BINARY= BUILT_FROM= SEARCH_RATE=0.25 ADD_RATE=0.5 USERS=4 SEED_ADDS=10 ERROR_STATUS=503 PORT_BASE=18700
readonly PRE=10 POST=15 RECOVER_LIMIT=180 MESSAGES=10 DRAIN_LIMIT=600 HELD_LIMIT=90
readonly RESIDUAL_LIMIT=${AML_DRILL_RESIDUAL_LIMIT:-10000}
# The minimum Search counts of the table are for this rate.
readonly TABLE_RATE=0.25

usage() {
	sed -n '7,9p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
}
while [ $# -gt 0 ]; do
	case $1 in
	--out) OUT=$2 ;;
	--only) ONLY=$2 ;;
	--repeat) REPEAT=$2 ;;
	--binary) BINARY=$2 ;;
	--built-from) BUILT_FROM=$2 ;;
	--search-rate) SEARCH_RATE=$2 ;;
	--add-rate) ADD_RATE=$2 ;;
	--users) USERS=$2 ;;
	--seed-adds) SEED_ADDS=$2 ;;
	--error-status) ERROR_STATUS=$2 ;;
	--port-base) PORT_BASE=$2 ;;
	*) usage ;;
	esac
	[ $# -ge 2 ] || usage
	shift 2
done
case $ERROR_STATUS in 500 | 502 | 503 | 529) ;; *) aml_die "--error-status must be 500, 502, 503 or 529" ;; esac
[[ $RESIDUAL_LIMIT =~ ^[0-9]+$ ]] || aml_die "AML_DRILL_RESIDUAL_LIMIT must be a whole number"
[ -z "${AML_DRILL_SCENARIOS:-}" ] || [ -r "$AML_DRILL_SCENARIOS" ] || aml_die "AML_DRILL_SCENARIOS names no readable file"
[[ $REPEAT =~ ^[1-9][0-9]*$ ]] || aml_die "--repeat must be a whole number of at least 1"
[[ $USERS =~ ^[1-9][0-9]*$ ]] || aml_die "--users must be a whole number of at least 1"
[[ $SEED_ADDS =~ ^[0-9]+$ ]] || aml_die "--seed-adds must be a whole number"
for rate in "$SEARCH_RATE" "$ADD_RATE"; do
	awk -v r="$rate" 'BEGIN { exit !(r + 0 > 0 && r == r + 0) }' || aml_die "rates must be positive numbers"
done
if [ -n "$BINARY" ]; then
	[ -x "$BINARY" ] || aml_die "--binary $BINARY is not an executable file"
	BINARY=$(cd "$(dirname "$BINARY")" && pwd)/$(basename "$BINARY")
elif [ -n "$BUILT_FROM" ]; then
	aml_die "--built-from goes with --binary"
fi

# The scenarios, one a line (see the header for the columns). Fault types:
#   toxic SPEC: PROXIES TOXIC STREAM TOXICITY ATTRIBUTES (a toxiproxy toxic)
#   down SPEC:  PROXIES (toxiproxy proxies disabled: connections refused)
#   fake SPEC:  KIND=PROFILE … (POST /_fault on the fake provider)
#   none SPEC:  - (no fault)
# PROXIES and KIND: deepseek, jev, both or all (both kinds). A rate of
# malformed replies applies to the replies of both kinds; a JEV reply is
# malformed as its kind can be, and so is a DeepSeek one (invalid-json only
# where the caller parses JSON: the extraction).
scenarios() {
	if [ -n "${AML_DRILL_SCENARIOS:-}" ]; then
		grep -v '^[[:space:]]*\(#\|$\)' "$AML_DRILL_SCENARIOS"
		return
	fi
	cat <<EOF
baseline|hard|60|none|-|12|none|0|0|-|steady|-|no fault: the workload alone
outage-30s|hard|30|toxic|both timeout downstream 1 {"timeout":0}|8|retries|0|0|-|-|-|blackhole on both providers for 30 s: connections accepted, nothing answered
429-retry-after|hard|12|fake|all={"errorRate":1,"errorStatus":429,"retryAfter":"5"}|5|throttled|0|0|-|steady|-|every call of both kinds 429 with Retry-After 5 for 12 s
429-no-retry-after|hard|12|fake|all={"errorRate":1,"errorStatus":429}|5|throttled|0|0|-|steady|-|every call of both kinds 429 without Retry-After for 12 s (3 patient retries)
429-no-retry-after-short|hard|20|fake|all={"errorRate":1,"errorStatus":429}|6|throttled|0|0|-|steady|-|every call of both kinds 429 without Retry-After for 20 s, under the 30 s a call (4 patient retries)
slow-past-timeout|hard|40|toxic|both latency downstream 1 {"latency":50000}|9|retries|0|0|-|-|-|every reply of both kinds 50 s late (past both attempt timeouts) for 40 s
resets|hard|30|toxic|both reset_peer downstream 0.3 {"timeout":0}|8|retries|0|0|-|-|-|30% of the connections to both providers reset for 30 s
latency-2x|measured|60|fake|deepseek={"latency":"4s"} jev={"latency":"3s"}|12|fake:all:slow|-|-|-|-|-|latency doubled (DeepSeek 4 s, JEV 3 s), below the attempt timeouts
latency-4x|measured|60|fake|deepseek={"latency":"8s"} jev={"latency":"6s"}|12|fake:all:slow|-|-|-|-|-|latency 4x (DeepSeek 8 s, JEV 6 s), below the attempt timeouts
jev-spikes-5|measured|60|fake|jev={"spikeRate":0.05,"spikeLatency":"25s"}|12|fake:jev:spike|-|-|-|-|-|5% of JEV calls 25 s slower (past the 20 s attempt timeout)
jev-spikes-20|measured|60|fake|jev={"spikeRate":0.2,"spikeLatency":"25s"}|12|fake:jev:spike|-|-|-|-|-|20% of JEV calls 25 s slower
deepseek-spikes-5|measured|60|fake|deepseek={"spikeRate":0.05,"spikeLatency":"50s"}|12|fake:deepseek:spike|-|-|-|-|-|5% of DeepSeek calls 50 s slower (past the 45 s attempt timeout)
deepseek-spikes-20|measured|60|fake|deepseek={"spikeRate":0.2,"spikeLatency":"50s"}|12|fake:deepseek:spike|-|-|-|-|-|20% of DeepSeek calls 50 s slower
5xx-10|measured|60|fake|all={"errorRate":0.1,"errorStatus":$ERROR_STATUS}|12|retries|-|-|-|-|-|10% of the calls of both kinds answered $ERROR_STATUS
5xx-30|measured|60|fake|all={"errorRate":0.3,"errorStatus":$ERROR_STATUS}|12|retries|-|-|-|-|-|30% of the calls of both kinds answered $ERROR_STATUS
malformed-invalid-json-5|hard|45|fake|all={"malformedRate":0.05,"malformed":"invalid-json"}|10|invalid|0|-|-|steady|-|5% of the replies of both kinds prose where the caller parses JSON (JEV; DeepSeek extraction)
malformed-invalid-json-20|measured|45|fake|all={"malformedRate":0.2,"malformed":"invalid-json"}|10|invalid|-|-|-|steady|-|20% of the replies of both kinds prose where the caller parses JSON
malformed-empty-5|hard|45|fake|all={"malformedRate":0.05,"malformed":"empty"}|10|invalid|0|-|-|steady|-|5% of the replies of both kinds empty (DeepSeek: no choices or no content; JEV: answers {})
malformed-empty-20|measured|45|fake|all={"malformedRate":0.2,"malformed":"empty"}|10|invalid|-|-|-|steady|-|20% of the replies of both kinds empty
malformed-truncated-5|hard|45|fake|all={"malformedRate":0.05,"malformed":"truncated"}|10|invalid|0|-|-|steady|-|5% of the replies of both kinds cut short (DeepSeek: content cut at its limit; JEV: body cut mid-JSON)
malformed-truncated-20|measured|45|fake|all={"malformedRate":0.2,"malformed":"truncated"}|10|invalid|-|-|-|steady|-|20% of the replies of both kinds cut short
malformed-wrong-schema-5|hard|45|fake|all={"malformedRate":0.05,"malformed":"wrong-schema"}|10|invalid|0|-|-|steady|-|5% of the replies of both kinds valid JSON of the wrong schema
malformed-wrong-schema-20|measured|45|fake|all={"malformedRate":0.2,"malformed":"wrong-schema"}|10|invalid|-|-|-|steady|-|20% of the replies of both kinds valid JSON of the wrong schema
stalls-5|hard|60|fake|all={"stallRate":0.05}|12|retries|-|-|180|-|-|5% of the replies of both kinds send headers and half the body, then hang
402-balance-deepseek|expected|30|fake|deepseek={"errorRate":1,"errorStatus":402}|8|rejected:deepseek|>0|-|-|steady|0:deepseek|DeepSeek answers 402 Insufficient Balance for 30 s: the Searches then degrade
402-balance-jev|expected|30|fake|jev={"errorRate":1,"errorStatus":402}|8|rejected:jev|>0|-|-|steady|0:jev|JEV answers 402 Insufficient Balance for 30 s: the Searches then degrade
outage-sustained|expected|120|down|both|21|breaker-open|>0|-|-|cycle|-|connections refused on both providers for 120 s, longer than any retry
EOF
}

# The scenarios this run selects, in the table's order.
SELECTED=$(scenarios | while IFS='|' read -r name _; do
	# The leading parenthesis keeps bash 3.2 from ending $( at the pattern's.
	case ",$ONLY," in (",," | *",$name,"*) printf '%s\n' "$name" ;; esac
done)
[ -n "$SELECTED" ] || aml_die "no scenario matched --only $ONLY"
if [ -n "$ONLY" ]; then
	for name in $(tr ',' ' ' <<<"$ONLY"); do
		grep -qx -- "$name" <<<"$SELECTED" || aml_die "--only names $name, which is no scenario"
	done
fi
PARTIAL=$([ -n "$ONLY" ] && echo true || echo false)
NEED_TOXI=$(scenarios | awk -F'|' -v selected="$(tr '\n' ' ' <<<"$SELECTED")" '
	BEGIN { n = split(selected, s, " "); for (i = 1; i <= n; i++) want[s[i]] = 1 }
	want[$1] && ($4 == "toxic" || $4 == "down") { found = 1 } END { print found ? 1 : 0 }')

[ -n "$OUT" ] || OUT=$AML_REPO/.testdata/aml/drill-$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p "$OUT/scenarios"
OUT=$(cd "$OUT" && pwd)
aml_need curl jq awk od pgrep go
FAKE_ADDR=127.0.0.1:$PORT_BASE
TOXI_PORT=$((PORT_BASE + 1))
DS_PORT=$((PORT_BASE + 2))
JEV_PORT=$((PORT_BASE + 3))
API_PORT=$((PORT_BASE + 4))
ADMIN_PORT=$((PORT_BASE + 5))
for port in $PORT_BASE $TOXI_PORT $DS_PORT $JEV_PORT $API_PORT $ADMIN_PORT; do
	aml_port_free "$port" || aml_die "127.0.0.1:$port is in use; pick another --port-base"
done
BASE_URL=http://127.0.0.1:$API_PORT
AML_ADMIN_URL=http://127.0.0.1:$ADMIN_PORT
WORK=$(mktemp -d "${TMPDIR:-/tmp}/aml-drill.XXXXXX")
KEY_FILE=$WORK/api.key
AML_ADMIN_TOKEN_FILE=$WORK/admin.token
AML_FAKE_PID= AML_SERVE_PID= TOXI_PID=
# The current scenario's background jobs: they stop at their stop file, and
# the cleanup kills what is left of them before anything is removed.
SAMPLER_PID= SEARCHER_PID= ADDER_PID= STOPS=

cleanup() {
	local status=$? stop pid
	for stop in $STOPS; do
		[ -d "$WORK" ] && : >"$stop" 2>/dev/null || true
	done
	for pid in $SEARCHER_PID $ADDER_PID $SAMPLER_PID; do
		aml_kill_tree TERM "$pid"
	done
	for pid in $SEARCHER_PID $ADDER_PID $SAMPLER_PID; do
		wait "$pid" 2>/dev/null || true
	done
	aml_stop "$AML_SERVE_PID" "$TOXI_PID" "$AML_FAKE_PID"
	rm -rf "$WORK"
	exit "$(aml_exit_status "$status")"
}
trap cleanup EXIT
trap 'exit 2' INT TERM

aml_secret "$KEY_FILE"
aml_secret "$AML_ADMIN_TOKEN_FILE"
aml_secret "$WORK/provider.key"
aml_say "building the fake provider${BINARY:+; running mnemon $BINARY}"
aml_build_fake "$WORK"
if [ -z "$BINARY" ]; then
	aml_say "building mnemon from $AML_REPO"
	aml_build_mnemon "$WORK"
	BINARY=$WORK/mnemon
	BUILD=$(aml_provenance | jq -c '. + {binary: "built from the scripts tree"}')
else
	BUILD=$(jq -cn --arg rev "$BUILT_FROM" '{binary: "given (--binary)", commit: (if $rev == "" then null else $rev end), dirty: (if $rev == "" then null else false end)}')
fi
SCRIPTS=$(aml_provenance)

aml_say "starting the fake provider$([ "$NEED_TOXI" = 1 ] && echo ', toxiproxy' || true) and aml-serve"
aml_start_fake "$WORK/fakeprovider" "$FAKE_ADDR" 2s 1.5s "$OUT/fakeprovider.log"
DS_URL=http://$FAKE_ADDR/chat/completions JEV_URL=http://$FAKE_ADDR/v1/systemone

# toxi METHOD PATH [BODY]: one call of toxiproxy's HTTP API.
toxi() {
	[ "$NEED_TOXI" = 1 ] || return 0
	curl -fsS --max-time 10 -X "$1" -H 'Content-Type: application/json' ${3:+--data-binary "$3"} \
		"http://127.0.0.1:$TOXI_PORT$2" >/dev/null || aml_die "toxiproxy refused $1 $2"
}
if [ "$NEED_TOXI" = 1 ]; then
	LOG_LEVEL=warn go run "$AML_TOXIPROXY_SERVER" -host 127.0.0.1 -port "$TOXI_PORT" >"$OUT/toxiproxy.log" 2>&1 &
	TOXI_PID=$!
	aml_wait_http "http://127.0.0.1:$TOXI_PORT/version" 300 || aml_die "toxiproxy did not start (see $OUT/toxiproxy.log)"
	toxi POST /proxies "{\"name\":\"deepseek\",\"listen\":\"127.0.0.1:$DS_PORT\",\"upstream\":\"$FAKE_ADDR\",\"enabled\":true}"
	toxi POST /proxies "{\"name\":\"jev\",\"listen\":\"127.0.0.1:$JEV_PORT\",\"upstream\":\"$FAKE_ADDR\",\"enabled\":true}"
	DS_URL=http://127.0.0.1:$DS_PORT/chat/completions JEV_URL=http://127.0.0.1:$JEV_PORT/v1/systemone
fi
aml_config "$WORK/memory-config.json" "$DS_URL" "$JEV_URL" "$WORK/model-budget.db"
aml_start_serve "$BINARY" "$WORK" "$WORK/memory-config.json" "127.0.0.1:$API_PORT" "127.0.0.1:$ADMIN_PORT" "16 16 16 4" "$OUT/aml-serve.log"
aml_admin /ready >/dev/null || aml_die "aml-serve is not ready"

USER_IDS=
i=1
while [ "$i" -le "$USERS" ]; do
	USER_IDS="$USER_IDS drill-u$i"
	i=$((i + 1))
done
SESSION=1 # the next session number of the drill's Adds

# request ROUTE BODY RESULTS: one POST to the public listener with the API
# key; appends "route code seconds shed" to RESULTS, shed 1 for a deliberate
# 503 (Retry-After and "busy; retry").
request() {
	local out code_time code seconds shed=0
	[ -d "$WORK" ] || return 0
	out=$(mktemp "$WORK/request.XXXXXX") || return 0
	code_time=$(aml_bearer "$KEY_FILE" | curl --config - -sS -o "$out.body" -D "$out.head" -w '%{http_code} %{time_total}' \
		-X POST -H 'Content-Type: application/json' --data-binary "@$2" --max-time 1800 "$BASE_URL/$1" 2>/dev/null) || true
	read -r code seconds <<<"${code_time:-000 0}"
	if [ "$code" = 503 ] && grep -qi '^retry-after:' "$out.head" 2>/dev/null && grep -q 'busy; retry' "$out.body" 2>/dev/null; then
		shed=1
	fi
	[ -d "$WORK" ] && printf '%s %s %s %s\n' "$1" "${code:-000}" "${seconds:-0}" "$shed" >>"$3" || true
	rm -f "$out" "$out.body" "$out.head"
}

# drive ROUTE RATE RESULTS STOP NAME: a request every 1/RATE s until the file
# STOP exists (or the work directory is gone), then waits for those in
# flight. Searches cycle over the users and the questions; Adds take the
# bodies $WORK/adds/NAME-<i>.json.
drive() {
	local interval i=0 body users
	# shellcheck disable=SC2206
	users=($USER_IDS)
	interval=$(awk -v r="$2" 'BEGIN { printf "%.3f", 1 / r }')
	while [ -d "$WORK" ] && [ ! -e "$4" ]; do
		i=$((i + 1))
		if [ "$1" = search ]; then
			body=$WORK/search-$5-$i.json
			aml_search_body "${users[$(((i - 1) % ${#users[@]}))]}" "$i" >"$body" 2>/dev/null || break
		else
			body=$WORK/adds/$5-$i.json
			[ -e "$body" ] || break
		fi
		request "$1" "$body" "$3" &
		sleep "$interval"
	done
	wait
}

# sample FILE STOP: /admin/status every second, with the time, until STOP
# exists or the work directory is gone.
sample() {
	while [ -d "$WORK" ] && [ ! -e "$2" ]; do
		aml_admin /admin/status 2>/dev/null | jq -c --argjson t "$(date +%s)" \
			'{t: $t, breakers, breakerTrips, queue, degraded, partial, searches, adds}' >>"$1" 2>/dev/null || true
		sleep 1
	done
}

# status: the scheduler's work and the breakers, as "queued running
# backingOff maxBacklog worst-breaker".
status() {
	aml_admin /admin/status | jq -r '[.queue.queued, .queue.running, .queue.backingOff, .queue.maxBacklog,
		(if ([.breakers[]] | all(. == "closed")) then "closed" elif ([.breakers[]] | any(. == "open")) then "open" else "half-open" end)] | @tsv'
}

# settle LIMIT: waits up to LIMIT seconds for the scheduler to have no work:
# no user queued, running or backing off, so every full batch is folded (an
# idle user holds at most the tail its next Search folds). Prints the time
# and the largest backlog then, or nothing.
settle() {
	local deadline=$(($(date +%s) + $1)) queued running backing backlog breaker
	while :; do
		read -r queued running backing backlog breaker <<<"$(status)"
		if [ "$queued" = 0 ] && [ "$running" = 0 ] && [ "$backing" = 0 ]; then
			printf '%s %s\n' "$(date +%s)" "$backlog"
			return
		fi
		[ "$(date +%s)" -lt "$deadline" ] || return 0
		sleep 2
	done
}

# unheld LIMIT: waits up to LIMIT seconds for every account's held amount to
# be 0 (the requests a stall or a cut left in flight end on their own);
# prints the seconds it took, or nothing.
unheld() {
	local start deadline
	start=$(date +%s)
	deadline=$((start + $1))
	while :; do
		if aml_admin /admin/status | jq -e '[.budgets[].held | fabs] | all(. < 1e-9)' >/dev/null; then
			echo $(($(date +%s) - start))
			return
		fi
		[ "$(date +%s)" -lt "$deadline" ] || return 0
		sleep 2
	done
}

# warm NAME: before a scenario, the faults are cleared, the backlog drained
# and the breakers closed: rounds of one Add and one Search (not counted in
# the scenario) until all three providers have answered.
warm() {
	local round=0 breaker results=$WORK/warm.txt deadline=$(($(date +%s) + 600)) users
	# shellcheck disable=SC2206
	users=($USER_IDS)
	toxi POST /reset
	curl -fsS --max-time 10 -X DELETE "http://$FAKE_ADDR/_fault" >/dev/null
	while :; do
		breaker=$(status | cut -f5)
		if [ "$breaker" = closed ] && [ -n "$(settle 0)" ]; then
			return
		fi
		[ "$(date +%s)" -lt "$deadline" ] || aml_die "the server did not settle before $1"
		round=$((round + 1))
		aml_add_bodies "$WORK/warm" "warm-$1-$round" "${users[$((round % ${#users[@]}))]}" 1 2 "$SESSION"
		SESSION=$((SESSION + 1))
		request add "$WORK/warm/warm-$1-$round-1.json" "$results"
		aml_search_body "${users[$((round % ${#users[@]}))]}" "$round" >"$WORK/warm-search.json"
		request search "$WORK/warm-search.json" "$results"
		sleep 2
	done
}

# fault apply|clear TYPE SPEC WINDOW
fault() {
	local targets toxic stream toxicity attributes proxy item kind
	read -r targets toxic stream toxicity attributes <<<"$3"
	if [ "$targets" = both ]; then targets="deepseek jev"; fi
	case $2 in
	none) ;;
	toxic)
		for proxy in $targets; do
			if [ "$1" = apply ]; then
				toxi POST "/proxies/$proxy/toxics" "{\"name\":\"drill\",\"type\":\"$toxic\",\"stream\":\"$stream\",\"toxicity\":$toxicity,\"attributes\":$attributes}"
			else
				toxi DELETE "/proxies/$proxy/toxics/drill"
			fi
		done
		;;
	down)
		for proxy in $targets; do
			toxi POST "/proxies/$proxy" "{\"enabled\":$([ "$1" = apply ] && echo false || echo true)}"
		done
		;;
	fake)
		if [ "$1" = clear ]; then
			curl -fsS --max-time 10 -X DELETE "http://$FAKE_ADDR/_fault" >/dev/null
			return
		fi
		for item in $3; do
			kind=${item%%=*}
			# The profile also ends by itself, 30 s after the window.
			jq -c --arg kind "$kind" --argjson seconds $(($4 + 30)) '. + {kind: $kind, seconds: $seconds}' <<<"${item#*=}" |
				curl -fsS --max-time 10 -X POST --data-binary @- "http://$FAKE_ADDR/_fault" >/dev/null || aml_die "the fake provider refused $item"
		done
		;;
	esac
}

# codes RESULTS ROUTE: the route's answers as {"total","codes","non2xx","shed"}.
codes() {
	awk -v route="$2" '$1 == route { n++; c[$2]++; if ($4 == 1) shed++; else if ($2 !~ /^2/) bad++ }
		END { printf "{\"total\":%d,\"non2xx\":%d,\"shed\":%d,\"codes\":{", n, bad, shed
			sep = ""; for (k in c) { printf "%s\"%s\":%d", sep, k, c[k]; sep = "," } print "}}" }' "$1"
}

# latency RESULTS ROUTE: the route's percentiles, in seconds.
latency() {
	awk -v route="$2" '$1 == route { print $3 }' "$1" | aml_percentiles
}

# run NAME RUN CLASS WINDOW TYPE SPEC SEARCHES EFFECT DEGRADED PARTIAL P95
# BREAKER RETRIES WHAT: one run of a scenario; writes its summary.
run() {
	local name=$1 nth=$2 class=$3 window=$4 type=$5 spec=$6 min_searches=$7 effect=$8 want_degraded=$9
	local want_partial=${10} want_p95=${11} want_breaker=${12} want_retries=${13} what=${14}
	local dir=$OUT/scenarios/$1 tag=$1 adds stop t_start t_fault t_end settled t_idle residual held_seconds log_lines without
	if [ "$REPEAT" -gt 1 ]; then
		dir=$dir-r$nth tag=$tag-r$nth
	fi
	mkdir -p "$dir"
	warm "$tag"
	aml_say "scenario $tag ($class): $what"
	min_searches=$(awk -v n="$min_searches" -v r="$SEARCH_RATE" -v t="$TABLE_RATE" 'BEGIN { printf "%d", n * r / t }')
	adds=$(awk -v r="$ADD_RATE" -v s=$((PRE + window + RECOVER_LIMIT + 30)) 'BEGIN { printf "%d", r * s + 10 }')
	aml_add_bodies "$WORK/adds" "$tag" "$USER_IDS" "$adds" "$MESSAGES" "$SESSION"
	SESSION=$((SESSION + adds))
	stop=$WORK/stop-$tag
	STOPS="$STOPS $stop $stop.sampler"
	aml_admin /admin/status >"$dir/status-before.json"
	aml_metrics >"$dir/metrics-before.json"
	curl -fsS "http://$FAKE_ADDR/_stats" >"$dir/fake-before.json"
	log_lines=$(wc -l <"$OUT/aml-serve.log")
	: >"$dir/results.txt"
	: >"$dir/samples.jsonl"
	sample "$dir/samples.jsonl" "$stop.sampler" &
	SAMPLER_PID=$!
	t_start=$(date +%s)
	drive search "$SEARCH_RATE" "$dir/results.txt" "$stop" "$tag" &
	SEARCHER_PID=$!
	drive add "$ADD_RATE" "$dir/results.txt" "$stop" "$tag" &
	ADDER_PID=$!
	sleep "$PRE"
	t_fault=$(date +%s)
	fault apply "$type" "$spec" "$window"
	[ "${AML_DRILL_FAIL_AT:-}" != fault ] || aml_die "failing on purpose after the fault of $tag (AML_DRILL_FAIL_AT=fault)"
	sleep "$window"
	fault clear "$type" "$spec" "$window"
	t_end=$(date +%s)
	sleep "$POST"
	# A breaker closes only on calls, so the workload goes on until every
	# breaker has closed again, for at most RECOVER_LIMIT s after the fault.
	until [ "$(status | cut -f5)" = closed ] || [ $(($(date +%s) - t_end)) -ge "$RECOVER_LIMIT" ]; do
		sleep 2
	done
	: >"$stop"
	wait "$SEARCHER_PID" "$ADDER_PID" || true
	SEARCHER_PID= ADDER_PID=
	aml_say "  workload ended $(($(date +%s) - t_end)) s after the fault; waiting for the backlog to drain"
	settled=$(settle $((DRAIN_LIMIT - ($(date +%s) - t_end))))
	read -r t_idle residual <<<"${settled:-null null}"
	held_seconds=$(unheld "$HELD_LIMIT")
	: >"$stop.sampler"
	wait "$SAMPLER_PID" || true
	SAMPLER_PID=
	aml_admin /admin/status >"$dir/status-after.json"
	aml_metrics >"$dir/metrics-after.json"
	curl -fsS "http://$FAKE_ADDR/_stats" >"$dir/fake-after.json"
	without=$(tail -n +$((log_lines + 1)) "$OUT/aml-serve.log" | grep -c 'without JEV' || true)
	# Why the Searches degraded, from the log: each reason and its count.
	tail -n +$((log_lines + 1)) "$OUT/aml-serve.log" | jq -R 'fromjson? | .msg // "" | select(startswith("degraded search "))
		| sub("^degraded search [0-9]+ [(]user [0-9a-f]+[)]: "; "") | sub(" [(]after [0-9]+ attempts[)]"; "")' |
		jq -s 'group_by(.) | map({(.[0]): length}) | add // {}' >"$dir/degraded-reasons.json"
	jq -n --arg name "$name" --argjson run "$nth" --arg class "$class" --arg what "$what" --arg type "$type" --arg spec "$spec" \
		--argjson window "$window" --argjson tstart "$t_start" --argjson tfault "$t_fault" --argjson tend "$t_end" \
		--argjson tidle "$t_idle" --argjson residual "$residual" --argjson heldSeconds "${held_seconds:-null}" \
		--argjson drainLimit "$DRAIN_LIMIT" --argjson residualLimit "$RESIDUAL_LIMIT" --argjson recoverLimit "$RECOVER_LIMIT" \
		--argjson without "$without" --argjson minSearches "$min_searches" --arg effect "$effect" \
		--arg wantDegraded "$want_degraded" --arg wantPartial "$want_partial" --arg wantP95 "$want_p95" \
		--arg wantBreaker "$want_breaker" --arg wantRetries "$want_retries" \
		--argjson searchLatency "$(latency "$dir/results.txt" search)" --argjson addLatency "$(latency "$dir/results.txt" add)" \
		--argjson searchCodes "$(codes "$dir/results.txt" search)" --argjson addCodes "$(codes "$dir/results.txt" add)" \
		--slurpfile sb "$dir/status-before.json" --slurpfile sa "$dir/status-after.json" \
		--slurpfile mb "$dir/metrics-before.json" --slurpfile ma "$dir/metrics-after.json" \
		--slurpfile fb "$dir/fake-before.json" --slurpfile fa "$dir/fake-after.json" \
		--slurpfile samples "$dir/samples.jsonl" --slurpfile reasons "$dir/degraded-reasons.json" -f "$WORK/scenario.jq" >"$dir/scenario.json"
	aml_say "  $(jq -r '"\(.verdict); searches \(.searches.answered) (at least \(.expect.searches)), degraded \(.searches.degraded), partial \(.searches.partial), effect \(.effect.signal) \(.effect.value), search p95 \(.latency.search.p95 // "-") s, trips \(.breakerTrips), drain \(.backlog.drainSeconds // "none") s, held \(.held.after | map_values(if fabs < 1e-9 then 0 else . end))"' "$dir/scenario.json")"
}

# The summary of a scenario's run, with its criteria.
cat >"$WORK/scenario.jq" <<'JQ'
def outcomesList: ["ok", "throttled", "invalid", "rejected", "retryable", "failed", "breaker-open", "bulkhead-full", "budget"];
# A series is picked by its name and its label pairs, in any order.
def pick($m; $name; $labels): [$m | to_entries[] | select((.key | startswith($name + "{") or . == $name)
	and (.key as $k | all($labels[]; . as $l | $k | contains($l)))) | .value] | add // 0;
def delta($name; $labels): pick($ma[0]; $name; $labels) - pick($mb[0]; $name; $labels);
def outcomes($kind; $class): reduce outcomesList[] as $o ({};
	.[$o] = delta("mnemon_aml_model_attempts_total"; ["kind=\"\($kind)\"", "class=\"\($class)\"", "outcome=\"\($o)\""]));
def held($s; $kind): ([$s.budgets[] | select(.kind == $kind) | .held] | add // 0);
def states($kind): [$samples[] | {t: (.t - $tfault), state: .breakers[$kind]}]
	| reduce .[] as $x ([]; if length == 0 or .[-1].state != $x.state then . + [$x] else . end);
def fakeDelta: [$fa[0] | to_entries[] | .key as $k | {($k): ([.value | to_entries[] | {(.key): (.value - ($fb[0][$k][.key] // 0))}] | add // {} | with_entries(select(.value != 0)))}] | add // {};
def verdictOf($ok): if $ok then "pass" else "fail" end;
($sa[0]) as $a | ($sb[0]) as $b |
{
	name: $name, run: $run, class: $class, fault: $what, type: $type, spec: $spec, windowSeconds: $window,
	searches: {answered: ($a.searches - $b.searches), degraded: ($a.degraded - $b.degraded), degradedReasons: $reasons[0], partial: ($a.partial - $b.partial),
		partialStages: ([$ma[0] | to_entries[] | select(.key | startswith("mnemon_aml_search_partial_total")) |
			{(.key | capture("stage=\"(?<s>[^\"]*)\"").s): (.value - ($mb[0][.key] // 0))}] | add // {} | with_entries(select(.value > 0)))},
	adds: ($a.adds - $b.adds),
	latency: {search: $searchLatency, add: $addLatency},
	requests: {search: $searchCodes, add: $addCodes},
	attempts: {deepseek: {interactive: outcomes("deepseek"; "interactive"), flush: outcomes("deepseek"; "flush"), background: outcomes("deepseek"; "background")},
		jev: {interactive: outcomes("jev"; "interactive"), flush: outcomes("jev"; "flush"), background: outcomes("jev"; "background")}},
	retries: {deepseek: delta("mnemon_aml_model_retries_total"; ["kind=\"deepseek\""]), jev: delta("mnemon_aml_model_retries_total"; ["kind=\"jev\""])},
	rejected: {deepseek: (($a.rejectedByKind.deepseek // 0) - ($b.rejectedByKind.deepseek // 0)), jev: (($a.rejectedByKind.jev // 0) - ($b.rejectedByKind.jev // 0))},
	breakerTrips: {deepseek: (($a.breakerTrips.deepseek // 0) - ($b.breakerTrips.deepseek // 0)), jev: (($a.breakerTrips.jev // 0) - ($b.breakerTrips.jev // 0))},
	breakerTimeline: {deepseek: states("deepseek"), jev: states("jev")},
	batches: (reduce ("folded", "deferred", "dropped") as $o ({}; .[$o] = delta("mnemon_aml_consolidation_batches_total"; ["outcome=\"\($o)\""]))),
	linkFallbacks: ($a.linkFallbacks - $b.linkFallbacks),
	jobs: (reduce ("ok", "failed", "paused", "canceled") as $o ({}; .[$o] = delta("mnemon_aml_consolidation_jobs_total"; ["outcome=\"\($o)\""]))),
	shed: {add: delta("mnemon_aml_shed_total"; ["route=\"add\""]), search: delta("mnemon_aml_shed_total"; ["route=\"search\""])},
	held: {after: {deepseek: held($a; "deepseek"), jev: held($a; "jev")}, seconds: $heldSeconds},
	backlog: {max: ([$samples[].queue.maxBacklog] | max // 0), residual: $residual, drainSeconds: (if $tidle == null then null else $tidle - $tend end)},
	recovery: {
		openedDuringFault: ([$samples[] | select(.t >= $tfault and .t <= $tend + 2) | [.breakers[]] | any(. == "open")] | any),
		opened: ([$samples[] | [.breakers[]] | any(. == "open")] | any),
		breakersClosedSeconds: ([$samples[] | select(.t >= $tend and ([.breakers[]] | all(. == "closed"))) | .t - $tend] | first // null),
		lastDegradedSeconds: ([range(1; $samples | length) as $i | select($samples[$i].degraded > $samples[$i - 1].degraded) | $samples[$i].t - $tend] | last // null)
	},
	fakeProvider: fakeDelta,
	withoutJEVLines: $without
}
| .totals = (reduce ([.attempts[][]][]) as $c ({}; reduce ($c | to_entries[]) as $e (.; .[$e.key] += $e.value)))
| .totals.retries = (.retries.deepseek + .retries.jev)
| .effect = {signal: $effect, value: (
	if $effect == "none" then .totals.retries + .totals.failed + .totals["breaker-open"] + .totals.throttled + .totals.invalid + .totals.rejected
	elif $effect == "retries" then .totals.retries + .totals.failed + .totals["breaker-open"]
	elif ($effect | startswith("rejected:")) then .rejected[$effect | ltrimstr("rejected:")]
	elif ($effect | startswith("fake:")) then ($effect | split(":")) as $f
		| (if $f[1] == "all" then [.fakeProvider[][$f[2]] // 0] | add // 0 else .fakeProvider[$f[1]][$f[2]] // 0 end)
	else .totals[$effect] // 0 end)}
| .expect = {searches: $minSearches, effect: (if $effect == "none" then "0" else ">0" end), degraded: $wantDegraded, partial: $wantPartial,
	searchP95: $wantP95, breaker: $wantBreaker, retries: $wantRetries, drainSeconds: $drainLimit, residualBelow: $residualLimit}
| def bound($want; $value): if $want == "-" then null elif $want == "0" then $value == 0 elif $want == ">0" then $value > 0 else null end;
.criteria = ({
	searches: (.searches.answered >= $minSearches),
	effect: (if $effect == "none" then .effect.value == 0 else .effect.value > 0 end),
	non2xx: (.requests.search.non2xx + .requests.add.non2xx == 0),
	held: (.held.seconds != null),
	withoutJEV: (.withoutJEVLines == 0 and .linkFallbacks == 0),
	degraded: bound($wantDegraded; .searches.degraded),
	partial: bound($wantPartial; .searches.partial),
	searchP95: (if $wantP95 == "-" then null else (.latency.search.p95 // 0) <= ($wantP95 | tonumber) end),
	breaker: (if $wantBreaker == "cycle" then .recovery.openedDuringFault and .recovery.breakersClosedSeconds != null
		elif $wantBreaker == "steady" then (.breakerTrips.deepseek + .breakerTrips.jev == 0) and (.recovery.opened | not)
		else null end),
	retries: (if $wantRetries == "-" then null else ($wantRetries | split(":")[1]) as $k | .retries[$k] == 0 end),
	drain: (.backlog.drainSeconds != null and .backlog.drainSeconds <= $drainLimit),
	residual: (.backlog.residual != null and .backlog.residual < $residualLimit)
} | with_entries(select(.value != null) | .value |= verdictOf(.)))
| .validity = ["searches", "effect", "non2xx", "held", "withoutJEV"]
| .failed = [.criteria | to_entries[] | select(.value == "fail") | .key]
| .hardFailed = (if $class == "measured" then [.failed[] | select(IN(.; "searches", "effect", "non2xx", "held", "withoutJEV"))] else .failed end)
| .verdict = (if (.hardFailed | length) > 0 then "fail: " + (.hardFailed | join(", "))
	elif (.failed | length) > 0 then "measured: " + (.failed | join(", ")) + " did not hold"
	elif $class == "measured" then "measured: all criteria held"
	else "pass" end)
JQ

STARTED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
if [ "$SEED_ADDS" -gt 0 ]; then
	aml_say "seeding $USERS users with $SEED_ADDS Adds each"
	aml_add_bodies "$WORK/adds" seed "$USER_IDS" $((USERS * SEED_ADDS)) "$MESSAGES" "$SESSION"
	SESSION=$((SESSION + USERS * SEED_ADDS))
	i=1
	while [ "$i" -le $((USERS * SEED_ADDS)) ]; do
		request add "$WORK/adds/seed-$i.json" "$OUT/seed.txt"
		i=$((i + 1))
	done
	[ -n "$(settle 600)" ] || aml_die "the seed's consolidation did not drain"
	for user in $USER_IDS; do
		aml_search_body "$user" 0 >"$WORK/seed-search.json"
		request search "$WORK/seed-search.json" "$OUT/seed.txt"
	done
	awk '$2 !~ /^2/ { bad++ } END { exit bad > 0 }' "$OUT/seed.txt" || aml_die "the seed's requests failed (see $OUT/seed.txt)"
fi

RAN=
nth=1
while [ "$nth" -le "$REPEAT" ]; do
	while IFS='|' read -r name class window type spec searches effect degraded partial p95 breaker retries what; do
		grep -qx -- "$name" <<<"$SELECTED" || continue
		run "$name" "$nth" "$class" "$window" "$type" "$spec" "$searches" "$effect" "$degraded" "$partial" "$p95" "$breaker" "$retries" "$what"
		RAN="$RAN $name$([ "$REPEAT" -gt 1 ] && echo "-r$nth" || true)"
	done <<EOF
$(scenarios)
EOF
	nth=$((nth + 1))
done

# The drill's own check that no secret reached its output.
leaked=0
for secret in "$KEY_FILE" "$AML_ADMIN_TOKEN_FILE" "$WORK/provider.key"; do
	if grep -rqF -f "$secret" "$OUT"; then
		leaked=1
	fi
done

jq -n --arg started "$STARTED" --arg finished "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --argjson build "$BUILD" --argjson scripts "$SCRIPTS" \
	--arg go "$(go env GOVERSION 2>/dev/null || echo none)" --arg host "$(uname -sm)" --argjson cpus "$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 0)" \
	--argjson searchRate "$SEARCH_RATE" --argjson addRate "$ADD_RATE" --argjson users "$USERS" --argjson seedAdds "$SEED_ADDS" \
	--argjson repeat "$REPEAT" --argjson partial "$PARTIAL" --arg only "$ONLY" --argjson leaked "$leaked" \
	--slurpfile scenarios <(for tag in $RAN; do cat "$OUT/scenarios/$tag/scenario.json"; done) '
{
	kind: "aml-fault-drill", started: $started, finished: $finished,
	partialRun: $partial, selected: (if $partial then ($only | split(",")) else "all" end), repeat: $repeat,
	build: $build, scripts: $scripts, go: $go, host: $host, cpus: $cpus,
	tools: {toxiproxy: "v2.12.0, go run github.com/Shopify/toxiproxy/v2/cmd/server@v2.12.0, driven through its HTTP API with curl, when a scenario needs it"},
	setup: {
		server: "aml-serve --consolidate background --max-concurrent-adds 16 --max-concurrent-searches 16 --max-open-stores 16 --consolidation-workers 4, admin listener on loopback with a token",
		configuration: "deploy/aml/memory-config.industrial.json; only testEndpoint (loopback) and budgetFile changed",
		fakeLatency: {deepseek: "2s", jev: "1.5s"},
		workload: {searchesPerSecond: $searchRate, addsPerSecond: $addRate, users: $users, messagesPerAdd: 10, seedAddsPerUser: $seedAdds, preSeconds: 10, postSeconds: 15}
	},
	criteria: {
		validity: "searches (at least the table minimum), effect (the fault signal above 0, or none with no fault), non2xx (no non-2xx but deliberate 503 sheds), held (every account back to 0 within 90 s), withoutJEV (no line, no link fallback): they fail any run",
		outcome: "degraded and partial (0, >0 or -), searchP95 (at most the table bound), breaker (cycle or steady), retries (0 for a kind), drain (no scheduler work within 600 s of the fault end), residual (largest backlog then below 10,000 characters): they fail hard and expected scenarios"
	},
	secretsInOutput: ($leaked == 1),
	matrix: [$scenarios[] | {name, run, class, fault, window: .windowSeconds, searches: .searches.answered, minSearches: .expect.searches,
		degraded: .searches.degraded, partial: .searches.partial, degradedReasons: .searches.degradedReasons,
		searchP50: .latency.search.p50, searchP95: .latency.search.p95, searchP99: .latency.search.p99,
		addP50: .latency.add.p50, addP95: .latency.add.p95, addP99: .latency.add.p99,
		attempts: .totals, throttled: .totals.throttled, invalid: .totals.invalid, rejected: (.rejected.deepseek + .rejected.jev), rejectedByKind: .rejected,
		retries: .totals.retries, breakerTrips, breakersClosedSeconds: .recovery.breakersClosedSeconds,
		deferred: .batches.deferred, linkFallbacks, dropped: .batches.dropped,
		maxBacklog: .backlog.max, residualBacklog: .backlog.residual, drainSeconds: .backlog.drainSeconds, heldAfter: .held.after,
		non2xx: (.requests.search.non2xx + .requests.add.non2xx), shed: (.requests.search.shed + .requests.add.shed),
		withoutJEV: .withoutJEVLines, effect, failed, verdict}],
	scenarios: $scenarios,
	failed: [$scenarios[] | .name as $n | .run as $r | .hardFailed[] | "\($n)\(if $repeat > 1 then " (run \($r))" else "" end): \(.)"],
	measuredNotHeld: [$scenarios[] | select(.class == "measured" and (.failed | length) > 0) | "\(.name): \(.failed | join(", "))"]
}
| .pass = ((.failed | length) == 0 and ($leaked == 0))' >"$OUT/report.json"

jq -r '
	"| scenario | run | class | Searches (min) | degraded | partial | Search p50/p95/p99 s | Add p95 ms | ok/throttled/invalid/rejected/retryable/failed/breaker-open/bulkhead-full/budget | retries | trips ds/jev | closed s | drain s | residual | held ds/jev | non-2xx/shed | effect | verdict |",
	"|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|",
	(.matrix[] | "| \(.name) | \(.run) | \(.class) | \(.searches) (\(.minSearches)) | \(.degraded) | \(.partial) | \(.searchP50 // "-")/\(.searchP95 // "-")/\(.searchP99 // "-") | \(if .addP95 then (.addP95 * 1000 | round) else "-" end) | \(.attempts.ok)/\(.attempts.throttled)/\(.attempts.invalid)/\(.attempts.rejected)/\(.attempts.retryable)/\(.attempts.failed)/\(.attempts["breaker-open"])/\(.attempts["bulkhead-full"])/\(.attempts.budget) | \(.retries) | \(.breakerTrips.deepseek)/\(.breakerTrips.jev) | \(.breakersClosedSeconds // "-") | \(.drainSeconds // "none") | \(.residualBacklog // "-") | \(.heldAfter.deepseek | . * 10000 | round / 10000)/\(.heldAfter.jev | . * 10000 | round / 10000) | \(.non2xx)/\(.shed) | \(.effect.signal) \(.effect.value) | \(.verdict) |")' \
	"$OUT/report.json" >"$OUT/matrix.md"

aml_say "report: $OUT/report.json"
summary=$(jq -r '"\(.matrix | length) scenario runs\(if .partialRun then ", a partial run (--only)" else "" end); measured that did not hold: \(if (.measuredNotHeld | length) == 0 then "none" else "\(.measuredNotHeld | length) (\(.measuredNotHeld | join("; ")))" end)"' "$OUT/report.json")
if [ "$leaked" = 1 ]; then
	aml_say "FAIL: a secret appears in $OUT ($summary)"
	AML_EXIT_OURS=1
	exit 1
fi
if jq -e '.pass' "$OUT/report.json" >/dev/null; then
	aml_say "PASS: every hard criterion held ($summary)"
else
	jq -r '.failed[] | "FAIL " + .' "$OUT/report.json" >&2
	aml_say "FAIL: $(jq -r '.failed | length | "\(.) hard criteri\(if . == 1 then "on" else "a" end)"' "$OUT/report.json") did not hold ($summary)"
	AML_EXIT_OURS=1
	exit 1
fi
