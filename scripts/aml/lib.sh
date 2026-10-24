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

AML_TOXIPROXY_SERVER=github.com/Shopify/toxiproxy/v2/cmd/server@v2.12.0
AML_VEGETA_MODULE=github.com/tsenart/vegeta/v12@v12.13.0
AML_REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
AML_SHIPPED_CONFIG=$AML_REPO/deploy/aml/memory-config.industrial.json

aml_die() {
	printf '%s: %s\n' "${PROG:-aml}" "$*" >&2
	exit 2
}
aml_say() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

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

# aml_stop PID...: stops each process and its children (go run's binary), in
# order, waiting up to 40 s for each before killing it: aml-serve first, so
# that its drain still reaches the providers.
aml_stop() {
	local pid deadline
	for pid in "$@"; do
		[ -n "$pid" ] || continue
		pkill -TERM -P "$pid" 2>/dev/null || true
		kill -TERM "$pid" 2>/dev/null || true
		deadline=$(($(date +%s) + 40))
		while kill -0 "$pid" 2>/dev/null && [ "$(date +%s)" -lt "$deadline" ]; do
			sleep 0.5
		done
		pkill -KILL -P "$pid" 2>/dev/null || true
		kill -KILL "$pid" 2>/dev/null || true
		wait "$pid" 2>/dev/null || true
	done
}

# aml_build DIR: builds mnemon and the fake provider of this checkout into
# DIR.
aml_build() {
	(cd "$AML_REPO" && go build -o "$1/mnemon" . && go build -o "$1/fakeprovider" ./scripts/aml/fakeprovider) ||
		aml_die "the build failed"
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
