#!/bin/bash
# scripts/aml/lib.sh: what the AML verification scripts (drill.sh, load.sh)
# share. Sourced, never run. It runs with bash 3.2 (macOS) and later.
#
# Secrets: every key and token lives in a file of mode 0600 in a private
# temporary directory, is read with `read` (a builtin, so no command line
# holds it), reaches curl as a config on its standard input (--config -),
# and reaches aml-serve through its environment. Nothing here prints one.
#
# Tools run at pinned versions with `go run …@version`, never through go.mod:
# toxiproxy v2.12.0 (drill.sh) and vegeta v12.13.0 (load.sh; VEGETA may name a
# vegeta binary instead, for a host without Go).
#
# Exit codes, the scripts' own: 0 when every hard criterion held, 1 when one
# failed (or a secret reached the output), 2 when the run could not be made
# (a usage error, a tool or a server that failed, an interruption, an abort).
# Only a script that ends on purpose (aml_exit) exits 0 or 1. aml_exit_status,
# run on exit, turns every other status into 2: a failing command's own, and
# a 0 that no script chose. Bash 3.2 (macOS) leaves $? as the last command
# left it when a fatal error of the shell's own ends a script (an unbound
# variable, a bad substitution, a division by zero), so that an EXIT trap sees
# that status, usually 0, whatever it captures first.

AML_TOXIPROXY_SERVER=github.com/Shopify/toxiproxy/v2/cmd/server@v2.12.0
AML_VEGETA_MODULE=github.com/tsenart/vegeta/v12@v12.13.0
AML_REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
AML_SHIPPED_CONFIG=$AML_REPO/deploy/aml/memory-config.industrial.json

aml_die() {
	printf '%s: %s\n' "${PROG:-aml}" "$*" >&2
	exit 2
}
aml_say() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

# AML_EXIT_OURS is set (by aml_exit) just before a script ends on purpose,
# with the status it means: 0 when the run completed, 1 when a criterion
# failed.
AML_EXIT_OURS=

# aml_exit STATUS: ends the script, on purpose, with STATUS (0 or 1).
aml_exit() {
	AML_EXIT_OURS=1
	exit "$1"
}

# aml_exit_status STATUS: the status a script exits with, when its EXIT trap saw
# STATUS. 2 stands (a usage error, aml_die, an interruption). 0 and 1 stand
# only after aml_exit; any other status is a 2, saying so. A 0 needs the
# script's say-so because the trap cannot tell a script that ended from one
# that an error of the shell's own stopped (see above).
aml_exit_status() {
	if [ "$1" = 2 ] || { [ "$1" -le 1 ] && [ -n "$AML_EXIT_OURS" ]; }; then
		echo "$1"
		return
	fi
	if [ "$1" = 0 ]; then
		printf '%s: stopped before its end by an error of the shell'"'"'s own (an unbound variable, say)\n' "${PROG:-aml}" >&2
	else
		printf '%s: stopped by a command that failed (status %s)\n' "${PROG:-aml}" "$1" >&2
	fi
	echo 2
}

# aml_trap_exit: until a script's cleanup takes the EXIT trap over, any exit
# maps its status (aml_exit_status), and an interruption exits 2. A script
# sets it first, before it reads its arguments, so that a failure that early
# is a 2 as well.
aml_trap_exit() {
	trap 'aml_status_was=$?; exit "$(aml_exit_status "$aml_status_was")"' EXIT
	trap 'exit 2' INT TERM
}

# aml_test_unbound POINT: a test hook. With AML_TEST_UNBOUND=POINT it expands a
# variable that is not set, as a bug of a script's might, so that a test can
# check that the script still exits non-zero. A script has two points: early,
# right after its first EXIT trap, and late, right after its own cleanup is set.
aml_test_unbound() {
	if [ "${AML_TEST_UNBOUND:-}" = "$1" ]; then
		# shellcheck disable=SC2154
		: "$AML_TEST_UNSET"
	fi
}

# aml_need TOOL...: every tool is on PATH.
aml_need() {
	local tool
	for tool in "$@"; do
		command -v "$tool" >/dev/null 2>&1 || aml_die "$tool is required"
	done
}

# aml_secret FILE: a new random secret (64 hex digits after a prefix) in
# FILE, mode 0600.
aml_secret() {
	(
		umask 077
		printf 'aml-test-%s\n' "$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')" >"$1"
	)
}

# aml_private_copy SRC NAME DST: copies the secret of the file SRC into DST
# (mode 0600): the value of its line NAME=value when it has one (a line of
# an instance's env file), else its first line, without quotes around it.
aml_private_copy() {
	local line
	line=$(grep -m1 "^$2=" "$1" 2>/dev/null) || IFS= read -r line <"$1" || [ -n "$line" ] || aml_die "cannot read the secret file $1"
	line=${line#"$2"=}
	line=${line#\"}
	line=${line%\"}
	line=${line#\'}
	line=${line%\'}
	[ -n "$line" ] || aml_die "the secret file $1 holds no secret"
	(
		umask 077
		printf '%s\n' "$line" >"$3"
	)
}

# aml_bearer FILE: the curl config line that sends FILE's secret as a bearer
# token, for `curl --config -`.
aml_bearer() {
	local secret=
	IFS= read -r secret <"$1" || [ -n "$secret" ] || aml_die "cannot read the secret file $1"
	printf 'header = "Authorization: Bearer %s"\n' "$secret"
}

# aml_port_free PORT: nothing listens on 127.0.0.1:PORT.
aml_port_free() {
	! (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}

# aml_wait_http URL SECONDS: waits until URL answers 2xx.
aml_wait_http() {
	local deadline=$(($(date +%s) + $2))
	until curl -fsS -o /dev/null --max-time 2 "$1" 2>/dev/null; do
		[ "$(date +%s)" -lt "$deadline" ] || return 1
		sleep 0.5
	done
}

# aml_admin PATH [CURL OPTION...]: GET PATH on the admin listener
# ($AML_ADMIN_URL) with the token in $AML_ADMIN_TOKEN_FILE.
aml_admin() {
	aml_bearer "$AML_ADMIN_TOKEN_FILE" | curl --config - -fsS --max-time 10 "${@:2}" "$AML_ADMIN_URL$1"
}

# aml_metrics: the server's mnemon_aml_* series (histogram buckets left out)
# as one JSON object of "name{labels}": value.
aml_metrics() {
	aml_admin /metrics | jq -Rn '
		[inputs | select(startswith("mnemon_aml_") and (contains("_bucket{") | not))
		 | capture("^(?<series>[^ ]+) (?<value>[^ ]+)$") | {(.series): (.value | tonumber)}] | add // {}'
}

# aml_sum METRICS PATTERN: the sum of the series of METRICS (a file that
# aml_metrics wrote) whose "name{labels}" matches the regular expression.
aml_sum() {
	jq --arg pattern "$2" '[to_entries[] | select(.key | test($pattern)) | .value] | add // 0' "$1"
}

# aml_kill_tree SIGNAL PID: sends SIGNAL to PID and all its descendants: a
# run's jobs, the shells that drive requests and what they start. Each
# process is stopped before its children are listed, so that none it starts
# meanwhile escapes, and gets SIGNAL again once continued: a Go program
# (vegeta) loses a signal that arrives while it is stopped.
aml_kill_tree() {
	local child
	[ -n "$2" ] || return 0
	kill -STOP "$2" 2>/dev/null || return 0
	for child in $(pgrep -P "$2" 2>/dev/null); do
		aml_kill_tree "$1" "$child"
	done
	kill "-$1" "$2" 2>/dev/null || true
	kill -CONT "$2" 2>/dev/null || true
	kill "-$1" "$2" 2>/dev/null || true
}

# aml_stop PID...: stops each server in order with one TERM (and go run's
# binary under it), waiting up to 40 s for it before killing what is left:
# aml-serve first, so that its drain still reaches the providers. (A second
# TERM would end aml-serve at once, without its drain.)
aml_stop() {
	local pid child deadline
	for pid in "$@"; do
		[ -n "$pid" ] || continue
		for child in $(pgrep -P "$pid" 2>/dev/null); do
			kill -TERM "$child" 2>/dev/null || true
		done
		kill -TERM "$pid" 2>/dev/null || true
		deadline=$(($(date +%s) + 40))
		while kill -0 "$pid" 2>/dev/null && [ "$(date +%s)" -lt "$deadline" ]; do
			sleep 0.5
		done
		aml_kill_tree KILL "$pid"
		wait "$pid" 2>/dev/null || true
	done
}

# aml_build_fake DIR: builds the fake provider of these scripts into
# DIR/fakeprovider (standard library only).
aml_build_fake() {
	(cd "$AML_REPO" && go build -o "$1/fakeprovider" ./scripts/aml/fakeprovider) || aml_die "the fake provider's build failed"
}

# aml_build_mnemon DIR: builds mnemon from the tree these scripts are in
# into DIR/mnemon. A run against another build takes --binary instead.
aml_build_mnemon() {
	(cd "$AML_REPO" && go build -o "$1/mnemon" .) || aml_die "the build of mnemon failed"
}

# aml_provenance: where the build came from, as JSON: {"commit","dirty"} of
# the git tree these scripts are in (the scripts' tree), null outside one.
# An untracked file makes it dirty, as a changed one does: go build compiles
# an untracked .go file. Ignored files do not.
aml_provenance() {
	local commit dirty
	if commit=$(git -C "$AML_REPO" rev-parse HEAD 2>/dev/null); then
		dirty=$([ -n "$(git -C "$AML_REPO" status --porcelain 2>/dev/null)" ] && echo true || echo false)
		printf '{"commit":"%s","dirty":%s}\n' "$commit" "$dirty"
	else
		printf '{"commit":null,"dirty":null}\n'
	fi
}

# aml_config FILE DEEPSEEK_URL JEV_URL BUDGET_FILE: the shipped configuration
# with its three models' testEndpoint set (DEEPSEEK_URL for the planner and
# the consolidator, JEV_URL for the judge) and the budget file moved; its
# budgets, prompts and everything else are the shipped ones. It refuses a
# configuration in which any model could reach a real provider.
aml_config() {
	jq --arg deepseek "$2" --arg jev "$3" --arg budget "$4" '
		.planner.testEndpoint = $deepseek | .consolidator.testEndpoint = $deepseek |
		.judge.testEndpoint = $jev | .budgetFile = $budget' "$AML_SHIPPED_CONFIG" >"$1" || aml_die "cannot write $1"
	jq -e '[.planner, .judge, .consolidator] | all(.testEndpoint | type == "string" and test("^http://127\\.0\\.0\\.1:[0-9]+/"))' \
		"$1" >/dev/null || aml_die "$1 has a model without a loopback testEndpoint"
}

# aml_start_fake BIN ADDR DEEPSEEK_LATENCY JEV_LATENCY LOG: starts the fake
# provider and waits for it; AML_FAKE_PID is its process.
aml_start_fake() {
	"$1" -listen "$2" -deepseek-latency "$3" -jev-latency "$4" >"$5" 2>&1 &
	AML_FAKE_PID=$!
	aml_wait_http "http://$2/health" 30 || aml_die "the fake provider did not start (see $5)"
}

# aml_start_serve BIN DIR CONFIG LISTEN ADMIN LIMITS LOG: starts aml-serve
# with background consolidation, its data root DIR/data, its secrets read
# from DIR/api.key, DIR/admin.token and DIR/provider.key (throwaway values
# the fake provider accepts), and LIMITS, which are "ADDS SEARCHES
# OPEN_STORES WORKERS"; then waits for it. AML_SERVE_PID is its process.
aml_start_serve() {
	local adds searches stores workers
	read -r adds searches stores workers <<<"$6"
	(
		IFS= read -r AML_API_KEY <"$2/api.key"
		IFS= read -r MNEMON_ADMIN_TOKEN <"$2/admin.token"
		IFS= read -r DEEPSEEK_API_KEY <"$2/provider.key"
		TYPESAFE_API_KEY=$DEEPSEEK_API_KEY
		export AML_API_KEY MNEMON_ADMIN_TOKEN DEEPSEEK_API_KEY TYPESAFE_API_KEY
		exec "$1" memory-agent aml-serve --listen "$4" --admin-listen "$5" --admin-token-env MNEMON_ADMIN_TOKEN \
			--auth-key-env AML_API_KEY --data-root "$2/data" --memory-config "$3" --consolidate background \
			--consolidation-workers "$workers" --max-open-stores "$stores" --max-concurrent-adds "$adds" \
			--max-concurrent-searches "$searches" --log-format json
	) >"$7" 2>&1 &
	AML_SERVE_PID=$!
	aml_wait_http "http://$4/health" 60 || aml_die "aml-serve did not start (see $7)"
}

# The conversation the scripts add: sentences a user and an assistant say,
# about people, places and plans that the Searches ask about. ASCII without
# quotes or backslashes, so they need no JSON escaping.
AML_USER_SAYS='I adopted a dog named Biscuit from the shelter on Maple Street last week.|My sister Anna is flying to Lisbon in June for the robotics conference.|I started training for the Riverside half marathon and my goal is under two hours.|We moved the team offsite from Denver to Austin because of the snow forecast.|My doctor said my cholesterol is down to 180 after three months of the new diet.|I finally finished reading Middlemarch and I want something lighter next.|Our landlord raised the rent to 2400 dollars starting in September.|I switched my phone plan to the family plan with my brother Marco.|The kitchen renovation is delayed until March because the cabinets are back ordered.|I signed up for the Thursday evening pottery class at the community center.|My daughter Lily wants a telescope for her birthday on the twelfth of August.|I paid off the car loan this month, two years ahead of schedule.'
AML_ASSISTANT_SAYS='That is wonderful news, how is Biscuit settling in at home so far?|Lisbon in June sounds lovely, will she present a paper at the conference?|A sub two hour half marathon is a great goal, how many days a week do you run?|Austin should be much milder, do you need help planning the new agenda?|That is a real improvement, congratulations on sticking with the diet.|After Middlemarch a short comic novel could be a nice change of pace.|That is a steep increase, would you like to compare nearby listings?|The family plan should save you both money, did the numbers come out lower?|Back ordered cabinets are frustrating, is the contractor still on schedule otherwise?|Pottery is a relaxing hobby, do you know who teaches the Thursday class?|A telescope is a thoughtful gift, would a beginner refractor suit her?|Paying it off early frees up your budget, congratulations on reaching that.'
AML_QUESTIONS='What is the name of the dog I adopted?|Where is my sister Anna flying in June?|What is my goal for the half marathon?|Where did we move the team offsite?|What did my doctor say about my cholesterol?|What book did I finish reading?|How much is my new rent?|Who is on my family phone plan?|Why is the kitchen renovation delayed?|What class did I sign up for?|What does Lily want for her birthday?|When did I pay off the car loan?'

# aml_add_bodies DIR NAME USERS COUNT MESSAGES [FIRST]: writes COUNT Add
# bodies, DIR/NAME-<i>.json for i = 1…COUNT: request NAME-<i>, one session
# of MESSAGES messages for the user USERS names (a list "u1 u2 …", cycled),
# the session numbered FIRST (default 1) and on. Each message is two of the
# sentences above (about 150 characters); sessions are an hour apart from
# 2023-05-08, messages a minute.
aml_add_bodies() {
	mkdir -p "$1"
	awk -v dir="$1" -v name="$2" -v users="$3" -v count="$4" -v messages="$5" -v first="${6:-1}" \
		-v said="$AML_USER_SAYS" -v replied="$AML_ASSISTANT_SAYS" 'BEGIN {
		n = split(said, U, "|"); m = split(replied, A, "|"); k = split(users, who, " ")
		for (i = 1; i <= count; i++) {
			file = dir "/" name "-" i ".json"
			s = first + i - 1
			printf "{\"request_id\":\"%s-%d\",\"user_id\":\"%s\",\"session_id\":\"s%d\",\"messages\":[", name, i, who[(i - 1) % k + 1], s > file
			for (j = 0; j < messages; j++) {
				if (j % 2 == 0) text = U[(s * 7 + j) % n + 1] " " U[(s * 3 + j * 5 + 1) % n + 1]
				else text = A[(s * 7 + j) % m + 1] " " A[(s * 5 + j * 3 + 2) % m + 1]
				printf "%s{\"role\":\"%s\",\"content\":\"%s\",\"timestamp\":%.0f}", (j ? "," : ""), (j % 2 ? "assistant" : "user"), text, (1683554160 + s * 3600 + j * 60) * 1000 > file
			}
			printf "]}\n" > file
			close(file)
		}
	}' || aml_die "cannot write the Add bodies"
}

# aml_search_body USER N: a Search of USER with the N-th question.
aml_search_body() {
	local IFS='|' questions
	# shellcheck disable=SC2206
	questions=($AML_QUESTIONS)
	printf '{"query":"%s","user_id":"%s","top_k":100}\n' "${questions[$(($2 % ${#questions[@]}))]}" "$1"
}

# aml_percentiles: reads seconds, one a line, and prints
# {"n":…,"p50":…,"p95":…,"p99":…,"max":…} (nearest rank), or {"n":0}.
aml_percentiles() {
	sort -n | awk '{ v[NR] = $1 } END {
		if (NR == 0) { print "{\"n\":0}"; exit }
		split("50 95 99", q, " ")
		line = "{\"n\":" NR
		for (k = 1; k <= 3; k++) { r = int(NR * q[k] / 100); if (r < NR * q[k] / 100) r++; if (r < 1) r = 1; line = line ",\"p" q[k] "\":" v[r] }
		print line ",\"max\":" v[NR] "}"
	}'
}
