#!/bin/bash
# scripts/aml/drill.sh: the fault drill of aml-serve (hardening plan, Task 12
# Step 2, with ruling R58's scenarios), on this machine, with no real provider.
#
#   scripts/aml/drill.sh [--out DIR] [--only NAME,…] [--search-rate R]
#                        [--add-rate R] [--users N] [--error-status CODE]
#                        [--port-base PORT]
#
# It builds mnemon and the fake provider (scripts/aml/fakeprovider) from this
# checkout, starts the fake provider on loopback (DeepSeek replies in 2 s, JEV
# in 1.5 s), toxiproxy v2.12.0 in front of it (go run …/cmd/server@v2.12.0,
# driven through its HTTP API with curl: the CLI is a client of the same API,
# and curl lets the drill time each change and check its answer), and
# aml-serve: --consolidate background, the dedicated limits (16 Adds, 16
# Searches, 16 open stores, 4 workers), an admin listener on loopback with a
# token (--admin-token-env), and the shipped configuration with only its
# models' testEndpoint (toxiproxy) and its budget file changed. It seeds a few
# users, then runs every scenario against a steady workload: a Search every
# 1/R s and an Add (10 messages) every 1/R s, spread over the users. Each
# scenario: 10 s of workload, the fault for its window, 15 s more of workload
# (and on, up to 180 s, until every breaker has closed again),
# then the requests in flight end and the drill waits for the consolidation
# backlog to drain. It reads /metrics and /admin/status with the admin token
# (curl --config -, never an argument), samples /admin/status every second,
# and writes DIR/report.json (one matrix row a scenario, and each scenario's
# detail), DIR/matrix.md, and the logs. It exits 1 when a hard criterion
# failed, 2 when it could not run.
#
# The retry budget, from internal/memoryagent/resilience.go and the shipped
# configuration (no timeoutSeconds but the consolidator's 600, no
# attemptTimeoutSeconds, no maxAttempts):
#   - A Search's calls (Interactive: the planner, DeepSeek, and the judge,
#     JEV) make 4 attempts. An attempt waits 45 s for DeepSeek, 20 s for JEV
#     (their hard timeout is 90 s). Retries wait a random part of 0.5 s, 1 s,
#     2 s (3.5 s at most in all), and at least an endpoint's Retry-After, up
#     to 10 s each.
#   - A Search's flush and the background jobs (Flush, Background) make 3
#     attempts of the hard timeout (600 s DeepSeek, 90 s JEV), retried after
#     up to 2 s, 4 s, or a Retry-After up to 5 min.
#   - Each provider's breaker opens after 5 failed attempts in a row or 10 of
#     the last 20, refuses calls for 20 s, then lets 2 probes through and
#     closes after 2 successes; a refused attempt is not retried.
# So a Search's call outlasts a hang or a slow reply of up to 3 attempt
# timeouts (60 s JEV, 135 s DeepSeek), a 429 with Retry-After R ≤ 10 s for 3R,
# and a refused connection, a reset or a 5xx without Retry-After only for its
# backoff (at most 3.5 s). The hard scenarios are sized under that budget:
# a 30 s blackhole, a 12 s 429 storm with Retry-After 5, replies 50 s late
# (past both attempt timeouts) for 40 s, and 30% of connections reset for
# 30 s (a call fails only if all 4 attempts do, about 1%). They must have
# zero non-2xx answers but deliberate 503 sheds, zero degraded Searches, the
# backlog drained within 10 minutes of the fault's end, and no "without JEV"
# line. The measured scenarios (R58: inflated latency, tail spikes, 5xx, 429
# without Retry-After, malformed replies, stalls) record the same numbers as
# findings. The expected one, connections refused for 120 s, is longer than
# any retry: its Searches degrade; the other three criteria still hold.
set -euo pipefail
umask 077
export LC_ALL=C

readonly PROG=aml-drill
# shellcheck source=scripts/aml/lib.sh
. "$(dirname "$0")/lib.sh"

OUT=
ONLY=
SEARCH_RATE=0.25
ADD_RATE=0.5
USERS=4
ERROR_STATUS=503
PORT_BASE=18700
readonly PRE=10 POST=15 RECOVER_LIMIT=180 SEED_ADDS=10 MESSAGES=10 DRAIN_LIMIT=600

usage() {
	sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
}
while [ $# -gt 0 ]; do
	case $1 in
	--out) OUT=$2 ;;
	--only) ONLY=$2 ;;
	--search-rate) SEARCH_RATE=$2 ;;
	--add-rate) ADD_RATE=$2 ;;
	--users) USERS=$2 ;;
	--error-status) ERROR_STATUS=$2 ;;
	--port-base) PORT_BASE=$2 ;;
	*) usage ;;
	esac
	shift 2 || usage
done
case $ERROR_STATUS in 500 | 502 | 503 | 529) ;; *) aml_die "--error-status must be 500, 502, 503 or 529" ;; esac

# The scenarios: name|class|window (s)|fault type|its specification|what it is.
#   toxic SPEC: PROXIES TOXIC STREAM TOXICITY ATTRIBUTES (toxiproxy toxic)
#   down SPEC:  PROXIES (toxiproxy proxies disabled: connections refused)
#   fake SPEC:  KIND=PROFILE … (POST /_fault on the fake provider)
#   none SPEC:  - (no fault)
# PROXIES and KIND: deepseek, jev, both or all (both kinds).
scenarios() {
	cat <<EOF
baseline|measured|60|none|-|no fault: the workload alone, for reference
outage-30s|hard|30|toxic|both timeout downstream 1 {"timeout":0}|blackhole on both providers for 30 s: connections accepted, nothing answered
429-retry-after|hard|12|fake|all={"errorRate":1,"errorStatus":429,"retryAfter":"5"}|every call 429 with Retry-After 5 for 12 s
slow-past-timeout|hard|40|toxic|both latency downstream 1 {"latency":50000}|every reply 50 s late (past both attempt timeouts) for 40 s
resets|hard|30|toxic|both reset_peer downstream 0.3 {"timeout":0}|30% of connections reset for 30 s
latency-2x|measured|60|fake|deepseek={"latency":"4s"} jev={"latency":"3s"}|latency doubled (DeepSeek 4 s, JEV 3 s), below the attempt timeouts
latency-4x|measured|60|fake|deepseek={"latency":"8s"} jev={"latency":"6s"}|latency 4x (DeepSeek 8 s, JEV 6 s), below the attempt timeouts
jev-spikes-5|measured|60|fake|jev={"spikeRate":0.05,"spikeLatency":"25s"}|5% of JEV calls 25 s slower (past the 20 s attempt timeout)
jev-spikes-20|measured|60|fake|jev={"spikeRate":0.2,"spikeLatency":"25s"}|20% of JEV calls 25 s slower
deepseek-spikes-5|measured|60|fake|deepseek={"spikeRate":0.05,"spikeLatency":"50s"}|5% of DeepSeek calls 50 s slower (past the 45 s attempt timeout)
deepseek-spikes-20|measured|60|fake|deepseek={"spikeRate":0.2,"spikeLatency":"50s"}|20% of DeepSeek calls 50 s slower
5xx-10|measured|60|fake|all={"errorRate":0.1,"errorStatus":$ERROR_STATUS}|10% of calls answered $ERROR_STATUS
5xx-30|measured|60|fake|all={"errorRate":0.3,"errorStatus":$ERROR_STATUS}|30% of calls answered $ERROR_STATUS
429-no-retry-after|measured|12|fake|all={"errorRate":1,"errorStatus":429}|every call 429 without Retry-After for 12 s
malformed-invalid-json-5|measured|45|fake|all={"malformedRate":0.05,"malformed":"invalid-json"}|5% of replies prose where the caller parses JSON
malformed-invalid-json-20|measured|45|fake|all={"malformedRate":0.2,"malformed":"invalid-json"}|20% of replies prose where the caller parses JSON
malformed-empty-5|measured|45|fake|all={"malformedRate":0.05,"malformed":"empty"}|5% of replies empty (no choices or content; JEV answers {})
malformed-empty-20|measured|45|fake|all={"malformedRate":0.2,"malformed":"empty"}|20% of replies empty
malformed-truncated-5|measured|45|fake|all={"malformedRate":0.05,"malformed":"truncated"}|5% of replies cut mid-JSON
malformed-truncated-20|measured|45|fake|all={"malformedRate":0.2,"malformed":"truncated"}|20% of replies cut mid-JSON
malformed-wrong-schema-5|measured|45|fake|all={"malformedRate":0.05,"malformed":"wrong-schema"}|5% of replies valid JSON of the wrong schema
malformed-wrong-schema-20|measured|45|fake|all={"malformedRate":0.2,"malformed":"wrong-schema"}|20% of replies valid JSON of the wrong schema
stalls-5|measured|60|fake|all={"stallRate":0.05}|5% of replies send headers and half the body, then hang
outage-sustained|expected|120|down|both|connections refused on both providers for 120 s, longer than any retry
EOF
}

[ -n "$OUT" ] || OUT=$AML_REPO/.testdata/aml/drill-$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p "$OUT/scenarios"
OUT=$(cd "$OUT" && pwd)
aml_need go curl jq awk od pkill
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
STOPS=

cleanup() {
	local stop
	for stop in $STOPS; do : >"$stop"; done
	aml_stop "$AML_SERVE_PID" "$TOXI_PID" "$AML_FAKE_PID"
	rm -rf "$WORK"
}
trap cleanup EXIT
trap 'exit 2' INT TERM

aml_secret "$KEY_FILE"
aml_secret "$AML_ADMIN_TOKEN_FILE"
aml_secret "$WORK/provider.key"
aml_say "building mnemon and the fake provider"
aml_build "$WORK"

aml_say "starting the fake provider, toxiproxy and aml-serve"
aml_start_fake "$WORK/fakeprovider" "$FAKE_ADDR" 2s 1.5s "$OUT/fakeprovider.log"
LOG_LEVEL=warn go run "$AML_TOXIPROXY_SERVER" -host 127.0.0.1 -port "$TOXI_PORT" >"$OUT/toxiproxy.log" 2>&1 &
TOXI_PID=$!
aml_wait_http "http://127.0.0.1:$TOXI_PORT/version" 300 || aml_die "toxiproxy did not start (see $OUT/toxiproxy.log)"

# toxi METHOD PATH [BODY]: one call of toxiproxy's HTTP API.
toxi() {
	curl -fsS --max-time 10 -X "$1" -H 'Content-Type: application/json' ${3:+--data-binary "$3"} \
		"http://127.0.0.1:$TOXI_PORT$2" >/dev/null || aml_die "toxiproxy refused $1 $2"
}
toxi POST /proxies "{\"name\":\"deepseek\",\"listen\":\"127.0.0.1:$DS_PORT\",\"upstream\":\"$FAKE_ADDR\",\"enabled\":true}"
toxi POST /proxies "{\"name\":\"jev\",\"listen\":\"127.0.0.1:$JEV_PORT\",\"upstream\":\"$FAKE_ADDR\",\"enabled\":true}"
aml_config "$WORK/memory-config.json" "http://127.0.0.1:$DS_PORT/chat/completions" "http://127.0.0.1:$JEV_PORT/v1/systemone" "$WORK/model-budget.db"
aml_start_serve "$WORK/mnemon" "$WORK" "$WORK/memory-config.json" "127.0.0.1:$API_PORT" "127.0.0.1:$ADMIN_PORT" "16 16 16 4" "$OUT/aml-serve.log"
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
	out=$(mktemp "$WORK/request.XXXXXX")
	code_time=$(aml_bearer "$KEY_FILE" | curl --config - -sS -o "$out.body" -D "$out.head" -w '%{http_code} %{time_total}' \
		-X POST -H 'Content-Type: application/json' --data-binary "@$2" --max-time 1800 "$BASE_URL/$1" 2>/dev/null) || true
	read -r code seconds <<<"${code_time:-000 0}"
	if [ "$code" = 503 ] && grep -qi '^retry-after:' "$out.head" 2>/dev/null && grep -q 'busy; retry' "$out.body" 2>/dev/null; then
		shed=1
	fi
	printf '%s %s %s %s\n' "$1" "${code:-000}" "${seconds:-0}" "$shed" >>"$3"
	rm -f "$out" "$out.body" "$out.head"
}

# drive ROUTE RATE RESULTS STOP NAME: a request every 1/RATE s until the file
# STOP exists, then waits for those in flight. Searches cycle over the users
# and the questions; Adds take the bodies $WORK/adds/NAME-<i>.json.
drive() {
	local interval i=0 body users
	# shellcheck disable=SC2206
	users=($USER_IDS)
	interval=$(awk -v r="$2" 'BEGIN { printf "%.3f", 1 / r }')
	while [ ! -e "$4" ]; do
		i=$((i + 1))
		if [ "$1" = search ]; then
			body=$WORK/search-$5-$i.json
			aml_search_body "${users[$(((i - 1) % ${#users[@]}))]}" "$i" >"$body"
		else
			body=$WORK/adds/$5-$i.json
			[ -e "$body" ] || break
		fi
		request "$1" "$body" "$3" &
		sleep "$interval"
	done
	wait
}

# sample FILE STOP: /admin/status every second, with the time, until STOP.
sample() {
	while [ ! -e "$2" ]; do
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
# idle user holds at most the tail its next Search folds; the scheduler's
# backlog counts headers too, so it can read 10,000 or more for less than a
# batch of text). Prints the time it did, or nothing.
settle() {
	local deadline=$(($(date +%s) + $1)) queued running backing backlog breaker
	while :; do
		read -r queued running backing backlog breaker <<<"$(status)"
		if [ "$queued" = 0 ] && [ "$running" = 0 ] && [ "$backing" = 0 ]; then
			date +%s
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
	[ "$targets" = both ] && targets="deepseek jev"
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

# run NAME CLASS WINDOW TYPE SPEC WHAT: one scenario; writes its summary.
run() {
	local name=$1 class=$2 window=$3 type=$4 spec=$5 what=$6 dir=$OUT/scenarios/$1 adds stop sampler searcher adder
	local t_start t_fault t_end t_idle log_lines without
	mkdir -p "$dir"
	warm "$name"
	aml_say "scenario $name ($class): $what"
	adds=$(awk -v r="$ADD_RATE" -v s=$((PRE + window + RECOVER_LIMIT + 30)) 'BEGIN { printf "%d", r * s + 10 }')
	aml_add_bodies "$WORK/adds" "$name" "$USER_IDS" "$adds" "$MESSAGES" "$SESSION"
	SESSION=$((SESSION + adds))
	stop=$WORK/stop-$name
	STOPS="$STOPS $stop"
	aml_admin /admin/status >"$dir/status-before.json"
	aml_metrics >"$dir/metrics-before.json"
	curl -fsS "http://$FAKE_ADDR/_stats" >"$dir/fake-before.json"
	log_lines=$(wc -l <"$OUT/aml-serve.log")
	: >"$dir/results.txt"
	sample "$dir/samples.jsonl" "$stop.sampler" &
	sampler=$!
	STOPS="$STOPS $stop.sampler"
	t_start=$(date +%s)
	drive search "$SEARCH_RATE" "$dir/results.txt" "$stop" "$name" &
	searcher=$!
	drive add "$ADD_RATE" "$dir/results.txt" "$stop" "$name" &
	adder=$!
	sleep "$PRE"
	t_fault=$(date +%s)
	fault apply "$type" "$spec" "$window"
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
	wait "$searcher" "$adder" || true
	aml_say "  workload ended $(($(date +%s) - t_end)) s after the fault; waiting for the backlog to drain"
	t_idle=$(settle $((DRAIN_LIMIT - ($(date +%s) - t_end))))
	: >"$stop.sampler"
	wait "$sampler" || true
	aml_admin /admin/status >"$dir/status-after.json"
	aml_metrics >"$dir/metrics-after.json"
	curl -fsS "http://$FAKE_ADDR/_stats" >"$dir/fake-after.json"
	without=$(tail -n +$((log_lines + 1)) "$OUT/aml-serve.log" | grep -c 'without JEV' || true)
	# Why the Searches degraded, from the log: each reason and its count.
	tail -n +$((log_lines + 1)) "$OUT/aml-serve.log" | jq -R 'fromjson? | .msg // "" | select(startswith("degraded search "))
		| sub("^degraded search [0-9]+ [(]user [0-9a-f]+[)]: "; "") | sub(" [(]after [0-9]+ attempts[)]"; "")' |
		jq -s 'group_by(.) | map({(.[0]): length}) | add // {}' >"$dir/degraded-reasons.json"
	jq -n --arg name "$name" --arg class "$class" --arg what "$what" --arg type "$type" --arg spec "$spec" \
		--argjson window "$window" --argjson tstart "$t_start" --argjson tfault "$t_fault" --argjson tend "$t_end" \
		--argjson tidle "${t_idle:-null}" --argjson drainLimit "$DRAIN_LIMIT" --argjson without "$without" \
		--argjson searchLatency "$(latency "$dir/results.txt" search)" --argjson addLatency "$(latency "$dir/results.txt" add)" \
		--argjson searchCodes "$(codes "$dir/results.txt" search)" --argjson addCodes "$(codes "$dir/results.txt" add)" \
		--slurpfile sb "$dir/status-before.json" --slurpfile sa "$dir/status-after.json" \
		--slurpfile mb "$dir/metrics-before.json" --slurpfile ma "$dir/metrics-after.json" \
		--slurpfile fb "$dir/fake-before.json" --slurpfile fa "$dir/fake-after.json" \
		--slurpfile samples "$dir/samples.jsonl" --slurpfile reasons "$dir/degraded-reasons.json" -f /dev/stdin >"$dir/scenario.json" <<'JQ'
# A series is picked by its name and its label pairs, in any order.
def pick($m; $name; $labels): [$m | to_entries[] | select((.key | startswith($name + "{") or . == $name)
	and (.key as $k | all($labels[]; . as $l | $k | contains($l)))) | .value] | add // 0;
def delta($name; $labels): pick($ma[0]; $name; $labels) - pick($mb[0]; $name; $labels);
def outcomes($kind; $class): reduce ("ok", "retryable", "failed", "breaker-open", "bulkhead-full", "budget") as $o ({};
	.[$o] = delta("mnemon_aml_model_attempts_total"; ["kind=\"\($kind)\"", "class=\"\($class)\"", "outcome=\"\($o)\""]));
def held($s; $kind): ([$s.budgets[] | select(.kind == $kind) | .held] | add // 0);
def states($kind): [$samples[] | {t: (.t - $tfault), state: .breakers[$kind]}]
	| reduce .[] as $x ([]; if length == 0 or .[-1].state != $x.state then . + [$x] else . end);
($sa[0]) as $a | ($sb[0]) as $b | ($tend - $tfault) as $faultEnd |
{
	name: $name, class: $class, fault: $what, type: $type, spec: $spec, windowSeconds: $window,
	searches: {answered: ($a.searches - $b.searches), degraded: ($a.degraded - $b.degraded), degradedReasons: $reasons[0], partial: ($a.partial - $b.partial),
		partialStages: ([$ma[0] | to_entries[] | select(.key | startswith("mnemon_aml_search_partial_total")) |
			{(.key | capture("stage=\"(?<s>[^\"]*)\"").s): (.value - ($mb[0][.key] // 0))}] | add // {} | with_entries(select(.value > 0)))},
	adds: ($a.adds - $b.adds),
	latency: {search: $searchLatency, add: $addLatency},
	requests: {search: $searchCodes, add: $addCodes},
	attempts: {deepseek: {interactive: outcomes("deepseek"; "interactive"), flush: outcomes("deepseek"; "flush"), background: outcomes("deepseek"; "background")},
		jev: {interactive: outcomes("jev"; "interactive"), flush: outcomes("jev"; "flush"), background: outcomes("jev"; "background")}},
	retries: {deepseek: delta("mnemon_aml_model_retries_total"; ["kind=\"deepseek\""]), jev: delta("mnemon_aml_model_retries_total"; ["kind=\"jev\""])},
	breakerTrips: {deepseek: (($a.breakerTrips.deepseek // 0) - ($b.breakerTrips.deepseek // 0)), jev: (($a.breakerTrips.jev // 0) - ($b.breakerTrips.jev // 0))},
	breakerTimeline: {deepseek: states("deepseek"), jev: states("jev")},
	batches: (reduce ("folded", "deferred", "dropped") as $o ({}; .[$o] = delta("mnemon_aml_consolidation_batches_total"; ["outcome=\"\($o)\""]))),
	linkFallbacks: ($a.linkFallbacks - $b.linkFallbacks),
	jobs: (reduce ("ok", "failed", "paused", "canceled") as $o ({}; .[$o] = delta("mnemon_aml_consolidation_jobs_total"; ["outcome=\"\($o)\""]))),
	shed: {add: delta("mnemon_aml_shed_total"; ["route=\"add\""]), search: delta("mnemon_aml_shed_total"; ["route=\"search\""])},
	budgetHeld: {deepseek: (held($a; "deepseek") - held($b; "deepseek")), jev: (held($a; "jev") - held($b; "jev"))},
	backlog: {max: ([$samples[].queue.maxBacklog] | max // 0), drainSeconds: (if $tidle == null then null else $tidle - $tend end)},
	recovery: {
		breakersClosedSeconds: ([$samples[] | select(.t >= $tend and ([.breakers[]] | all(. == "closed"))) | .t - $tend] | first // null),
		lastDegradedSeconds: ([range(1; $samples | length) as $i | select($samples[$i].degraded > $samples[$i - 1].degraded) | $samples[$i].t - $tend] | last // null)
	},
	fakeProvider: ([$fa[0] | to_entries[] | .key as $k | {($k): ([.value | to_entries[] | {(.key): (.value - ($fb[0][$k][.key] // 0))}] | add // {} | with_entries(select(.value != 0)))}] | add),
	withoutJEVLines: $without
}
| .criteria = {
	non2xx: (if .requests.search.non2xx + .requests.add.non2xx == 0 then "pass" else "fail" end),
	degraded: (if .searches.degraded == 0 then "pass" elif $class == "expected" then "expected" else "fail" end),
	backlog: (if .backlog.drainSeconds != null and .backlog.drainSeconds <= $drainLimit then "pass" else "fail" end),
	withoutJEV: (if .withoutJEVLines == 0 and .linkFallbacks == 0 then "pass" else "fail" end)
}
| .failed = [.criteria | to_entries[] | select(.value == "fail") | .key]
| .verdict = (if $class == "measured" then (if (.failed | length) == 0 then "measured: all criteria held" else "measured: " + (.failed | join(", ")) + " did not hold" end)
	elif (.failed | length) == 0 then "pass" else "fail: " + (.failed | join(", ")) end)
JQ
	aml_say "  $(jq -r '"\(.verdict); searches \(.searches.answered), degraded \(.searches.degraded), partial \(.searches.partial), search p95 \(.latency.search.p95 // "-") s, trips \(.breakerTrips), drain \(.backlog.drainSeconds // "none") s"' "$dir/scenario.json")"
}

STARTED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
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

ran=
while IFS='|' read -r name class window type spec what; do
	case ",$ONLY," in ",,"|*",$name,"*) ;; *) continue ;; esac
	run "$name" "$class" "$window" "$type" "$spec" "$what"
	ran="$ran $name"
done <<EOF
$(scenarios)
EOF
[ -n "$ran" ] || aml_die "no scenario matched --only $ONLY"

# The drill's own check that no secret reached its output.
leaked=0
for secret in "$KEY_FILE" "$AML_ADMIN_TOKEN_FILE" "$WORK/provider.key"; do
	if grep -rqF -f "$secret" "$OUT"; then
		leaked=1
	fi
done

jq -n --arg started "$STARTED" --arg finished "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
	--arg commit "$(git -C "$AML_REPO" rev-parse --short HEAD 2>/dev/null || echo unknown)" \
	--arg go "$(go env GOVERSION)" --arg host "$(uname -sm)" --argjson cpus "$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 0)" \
	--argjson searchRate "$SEARCH_RATE" --argjson addRate "$ADD_RATE" --argjson users "$USERS" --argjson leaked "$leaked" \
	--slurpfile scenarios <(for name in $ran; do cat "$OUT/scenarios/$name/scenario.json"; done) '
{
	kind: "aml-fault-drill", started: $started, finished: $finished, commit: $commit, go: $go, host: $host, cpus: $cpus,
	tools: {toxiproxy: "v2.12.0, go run github.com/Shopify/toxiproxy/v2/cmd/server@v2.12.0, driven through its HTTP API with curl"},
	setup: {
		server: "aml-serve --consolidate background --max-concurrent-adds 16 --max-concurrent-searches 16 --max-open-stores 16 --consolidation-workers 4, admin listener on loopback with a token",
		configuration: "deploy/aml/memory-config.industrial.json; only testEndpoint (toxiproxy, loopback) and budgetFile changed",
		fakeLatency: {deepseek: "2s", jev: "1.5s"},
		workload: {searchesPerSecond: $searchRate, addsPerSecond: $addRate, users: $users, messagesPerAdd: 10, preSeconds: 10, postSeconds: 15}
	},
	retryBudget: {
		interactive: "4 attempts; attempt timeout 45 s DeepSeek, 20 s JEV (hard 90 s); backoff random up to 0.5 s, 1 s, 2 s; Retry-After honoured up to 10 s",
		flushAndBackground: "3 attempts of the hard timeout (600 s DeepSeek consolidator, 90 s JEV); backoff up to 2 s, 4 s; Retry-After up to 5 min",
		breaker: "opens after 5 failed attempts in a row or 10 of the last 20; refuses 20 s; 2 probes; closes after 2 successes; refused attempts are not retried",
		coversForASearch: "hangs and slow replies up to 3 attempt timeouts (JEV 60 s, DeepSeek 135 s); 429 with Retry-After R up to 3R; refused, reset or 5xx without Retry-After only the backoff (at most 3.5 s)"
	},
	criteria: ["non2xx: zero non-2xx answers other than deliberate 503 sheds", "degraded: zero degraded Searches (hard scenarios; expected for the sustained outage)",
		"backlog: the scheduler has no work (no user queued, running or backing off: every full batch folded) within 600 s of the fault ending", "withoutJEV: no \"without JEV\" line and no link fallback"],
	secretsInOutput: ($leaked == 1),
	matrix: [$scenarios[] | {name, class, fault, searches: .searches.answered, degraded: .searches.degraded, degradedReasons: .searches.degradedReasons, partial: .searches.partial,
		searchP50: .latency.search.p50, searchP95: .latency.search.p95, searchP99: .latency.search.p99,
		addP50: .latency.add.p50, addP95: .latency.add.p95, addP99: .latency.add.p99,
		attempts: ([.attempts[][]] | reduce .[] as $c ({}; reduce ($c | to_entries[]) as $e (.; .[$e.key] += $e.value))),
		retries: (.retries.deepseek + .retries.jev), breakerTrips, deferred: .batches.deferred, linkFallbacks, dropped: .batches.dropped,
		maxBacklog: .backlog.max, drainSeconds: .backlog.drainSeconds, breakersClosedSeconds: .recovery.breakersClosedSeconds,
		non2xx: (.requests.search.non2xx + .requests.add.non2xx), shed: (.requests.search.shed + .requests.add.shed),
		withoutJEV: .withoutJEVLines, verdict}],
	scenarios: $scenarios,
	failed: [$scenarios[] | select(.class != "measured") | .name as $n | .failed[] | "\($n): \(.)"]
}
| .pass = ((.failed | length) == 0 and ($leaked == 0))' >"$OUT/report.json"

jq -r '
	"| scenario | class | Searches | degraded | partial | Search p50/p95/p99 s | Add p50/p95/p99 s | attempts ok/retryable/failed/breaker-open/bulkhead-full/budget | retries | trips ds/jev | deferred | link fallbacks | dropped | max backlog | drain s | non-2xx | shed | without JEV | verdict |",
	"|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|",
	(.matrix[] | "| \(.name) | \(.class) | \(.searches) | \(.degraded) | \(.partial) | \(.searchP50 // "-")/\(.searchP95 // "-")/\(.searchP99 // "-") | \(.addP50 // "-")/\(.addP95 // "-")/\(.addP99 // "-") | \(.attempts.ok // 0)/\(.attempts.retryable // 0)/\(.attempts.failed // 0)/\(.attempts["breaker-open"] // 0)/\(.attempts["bulkhead-full"] // 0)/\(.attempts.budget // 0) | \(.retries) | \(.breakerTrips.deepseek)/\(.breakerTrips.jev) | \(.deferred) | \(.linkFallbacks) | \(.dropped) | \(.maxBacklog) | \(.drainSeconds // "none") | \(.non2xx) | \(.shed) | \(.withoutJEV) | \(.verdict) |")' \
	"$OUT/report.json" >"$OUT/matrix.md"

aml_say "report: $OUT/report.json"
if [ "$leaked" = 1 ]; then
	aml_say "FAIL: a secret appears in $OUT"
	exit 1
fi
if jq -e '.pass' "$OUT/report.json" >/dev/null; then
	aml_say "PASS: every hard criterion held"
else
	jq -r '.failed[] | "FAIL " + .' "$OUT/report.json" >&2
	exit 1
fi
