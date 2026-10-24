#!/bin/bash
# mnemon-preflight <n>: checks that engine instance <n> of this host is ready
# to serve. Install once per host as /usr/local/sbin/mnemon-preflight and run
# it as root; `mnemon-instance new` runs it too. See docs/aml-adapter.md
# ("Preflight").
#
# It checks:
#   - env and watch.env: root's, mode 0600, holding every name they need;
#   - the public /health through Caddy, and the admin listener's /ready;
#   - /admin/status: a lag bound (background consolidation) and every
#     budget's spent and held below 80% of its cap, printing the numbers;
#   - no store of smoke-user, the runbook's smoke-test user, in the data root;
#   - only while its inspect service is on (inspect.env): inspect.env root's,
#     mode 0600, holding every name it needs, and the inspect listener's
#     /healthz answering 200 with its token;
#   - aml-serve@<n>, aml-watch@<n>.timer, aml-inspect@<n> while inspect is
#     on, and caddy active;
#   - systemd-analyze verify on the unit templates, and caddy validate, when
#     the tools are here;
#   - wg0 up with the AML_ADMIN_LISTEN address, and ufw active with the rules
#     of the runbook's "Firewall" and none that lets anyone reach 51820/udp or
#     an admin port.
#
# Every check runs. Each failure prints one line that starts with FAIL, and
# the exit status is 1 when any failed. The env files are read with grep and
# never sourced, no secret is printed, and the admin token reaches curl only
# as a config on its standard input (--config -), never as an argument.
#
# For tests, --root DIR reads every file under DIR (owned by the user running
# the tests), and --no-systemd skips the units, systemd-analyze, caddy, wg0
# and ufw.
set -euo pipefail
umask 077
export LC_ALL=C

readonly PROG=mnemon-preflight
readonly ETC=/etc/mnemon-aml
readonly STATE=/var/lib/mnemon-aml
readonly UNITS=/etc/systemd/system
readonly CADDYFILE=/etc/caddy/Caddyfile
# The hub of the WireGuard overlay: the ops host, where the console runs.
readonly HUB=10.77.0.1
# The share of a cap that spent and held may reach before a job.
readonly BUDGET_CEILING=0.80

ROOT=
SYSTEMD=1
NAME=
CHECKED=0
FAILED=0
BODY=

die() {
	printf '%s: %s\n' "$PROG" "$*" >&2
	exit 2
}
fail() {
	FAILED=$((FAILED + 1))
	printf 'FAIL %s\n' "$*"
}
info() { printf '  %s\n' "$*"; }
checking() { CHECKED=$((CHECKED + 1)); }

on_exit() {
	local status=$?
	[ -z "$BODY" ] || rm -f -- "$BODY"
	exit "$status"
}
trap on_exit EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

usage() {
	cat <<EOF
Usage: $PROG [--root DIR] [--no-systemd] <n>

Checks that engine instance <n> of this host is ready to serve. Run as root.
Every check runs; each failure prints one FAIL line, and the exit status is 1
if any failed. No secret is printed.

For tests: --root DIR reads every file under DIR; --no-systemd skips the
units, systemd-analyze, caddy, wg0 and ufw.
EOF
}

# need TOOL...: refuses to go on without each tool, naming its apt package.
need() {
	local tool missing=
	for tool in "$@"; do
		if ! command -v "$tool" >/dev/null 2>&1; then
			case $tool in
			curl | jq) missing="$missing $tool (apt install $tool)" ;;
			*) missing="$missing $tool (apt install coreutils)" ;;
			esac
		fi
	done
	[ -z "$missing" ] || die "missing tools:$missing"
}

# sha256_hex: the SHA-256 of standard input, in hex.
sha256_hex() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum
	else
		shasum -a 256
	fi | cut -d ' ' -f 1
}

# has FILE NAME: FILE has a NAME= line with a value.
has() { grep -Eq "^$2=.+" "$1" 2>/dev/null; }

# env_get FILE NAME: the value of FILE's last NAME= line, "" without one;
# one pair of quotes around it and trailing whitespace are dropped.
env_get() {
	local line
	line=$(grep -E "^$2=" "$1" 2>/dev/null | tail -n 1) || true
	line=${line#*=}
	while [[ $line == *[[:space:]] ]]; do
		line=${line%?}
	done
	case $line in
	\"*\") line=${line#\"} line=${line%\"} ;;
	\'*\') line=${line#\'} line=${line%\'} ;;
	esac
	printf '%s' "$line"
}

# telegram_url URL: URL is a Telegram bot's sendMessage with its chat, as the
# watcher requires: https://<host>/bot<token>/sendMessage?chat_id=<chat id>,
# chat_id its only parameter, a chat's number (negative for a group or a
# channel) or a channel's @username. The value never leaves the shell.
telegram_url() {
	local re='^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?/([^?#%[:space:]]*/)?sendMessage\?chat_id=(-?[0-9]{1,20}|(@|%40)[A-Za-z][A-Za-z0-9_]{4,31})$'
	[[ $1 =~ $re ]]
}

# owner_mode FILE: its owner's uid and its permission bits (GNU or BSD stat).
owner_mode() {
	stat -c '%u %a' "$1" 2>/dev/null || stat -f '%u %Lp' "$1"
}

# owner_uid: root, or under --root the user running the tests.
owner_uid() {
	if [ -n "$ROOT" ]; then id -u; else printf 0; fi
}

# answered CODE: what an HTTP status from curl says.
answered() {
	if [ "${1:-000}" = 000 ]; then printf 'did not answer'; else printf 'answered %s' "$1"; fi
}

# admin_get PATH: GETs PATH on the instance's admin listener with the admin
# token into $BODY, and prints the HTTP status ("000" without an answer).
admin_get() {
	local env="$ROOT$ETC/$NAME/env" code
	code=$(printf 'header = "Authorization: Bearer %s"\n' "$(env_get "$env" MNEMON_ADMIN_TOKEN)" |
		curl --disable --config - --silent --output "$BODY" --max-time 10 --write-out '%{http_code}' \
			"http://$(env_get "$env" AML_ADMIN_LISTEN)$1") || true
	printf '%s' "${code:-000}"
}

check_files() {
	local file owner mode
	for file in env watch.env; do
		checking
		if [ ! -f "$ROOT$ETC/$NAME/$file" ]; then
			fail "$ETC/$NAME/$file is missing"
			continue
		fi
		read -r owner mode <<EOF
$(owner_mode "$ROOT$ETC/$NAME/$file")
EOF
		[ "$owner" = "$(owner_uid)" ] || fail "$ETC/$NAME/$file is owned by uid $owner, not root"
		[ "$mode" = 600 ] || fail "$ETC/$NAME/$file has mode $mode, not 0600"
	done
}

# check_engine_env: the API key, the provider keys the instance's
# configuration names, the admin token, the listeners and the flag values.
check_engine_env() {
	checking
	local env="$ROOT$ETC/$NAME/env" config="$ETC/$NAME/memory-config.json" keys= var
	[ -f "$env" ] || return 0
	if ! keys=$(jq -r '[.. | objects | .keyEnv? | strings] | unique | .[]' "$ROOT$config" 2>/dev/null); then
		fail "$config is missing or not JSON"
		keys=
	fi
	for var in AML_API_KEY $keys MNEMON_ADMIN_TOKEN AML_LISTEN AML_ADMIN_LISTEN AML_MAX_ADDS AML_MAX_SEARCHES AML_MAX_OPEN_STORES AML_WORKERS; do
		has "$env" "$var" || fail "$ETC/$NAME/env lacks $var"
	done
}

# check_watch_env: the alert bot, the public URL, and the engine's admin
# listener and token.
check_watch_env() {
	checking
	local env="$ROOT$ETC/$NAME/env" watch="$ROOT$ETC/$NAME/watch.env" var
	[ -f "$watch" ] || return 0
	for var in AML_ALERT_WEBHOOK AML_ALERT_KIND AML_PUBLIC_URL AML_ADMIN_LISTEN MNEMON_ADMIN_TOKEN; do
		has "$watch" "$var" || fail "$ETC/$NAME/watch.env lacks $var"
	done
	case $(env_get "$watch" AML_ALERT_KIND) in
	telegram | feishu | wecom | "") ;;
	*) fail "$ETC/$NAME/watch.env: AML_ALERT_KIND is \"$(env_get "$watch" AML_ALERT_KIND)\", not telegram, feishu or wecom" ;;
	esac
	case $(env_get "$watch" AML_ALERT_WEBHOOK) in
	"") ;;
	https://?*)
		if [ "$(env_get "$watch" AML_ALERT_KIND)" = telegram ] && ! telegram_url "$(env_get "$watch" AML_ALERT_WEBHOOK)"; then
			fail "$ETC/$NAME/watch.env: AML_ALERT_WEBHOOK is not a Telegram bot's sendMessage URL with its chat: https://api.telegram.org/bot<token>/sendMessage?chat_id=<chat id>"
		fi
		;;
	*) fail "$ETC/$NAME/watch.env: AML_ALERT_WEBHOOK is not an https URL" ;;
	esac
	case $(env_get "$watch" AML_PUBLIC_URL) in
	https://?* | "") ;;
	*) fail "$ETC/$NAME/watch.env: AML_PUBLIC_URL is not an https URL" ;;
	esac
	[ "$(env_get "$watch" AML_ADMIN_LISTEN)" = "$(env_get "$env" AML_ADMIN_LISTEN)" ] ||
		fail "$ETC/$NAME/watch.env: AML_ADMIN_LISTEN differs from the engine's"
	[ "$(env_get "$watch" MNEMON_ADMIN_TOKEN)" = "$(env_get "$env" MNEMON_ADMIN_TOKEN)" ] ||
		fail "$ETC/$NAME/watch.env: MNEMON_ADMIN_TOKEN differs from the engine's"
}

check_health() {
	checking
	local url code
	url=$(env_get "$ROOT$ETC/$NAME/watch.env" AML_PUBLIC_URL)
	if [ -z "$url" ]; then
		fail "no public URL to probe (AML_PUBLIC_URL)"
		return 0
	fi
	code=$(curl --disable --silent --output /dev/null --max-time 10 --write-out '%{http_code}' "$url/health") || true
	[ "$code" = 200 ] || fail "GET $url/health through Caddy $(answered "$code"); want 200"
}

check_ready() {
	checking
	local code reasons
	code=$(admin_get /ready)
	[ "$code" = 200 ] && return 0
	reasons=$(jq -r '(.reasons // []) | join("; ")' "$BODY" 2>/dev/null) || reasons=
	fail "/ready on the admin listener $(answered "$code")${reasons:+: $reasons}"
}

check_status() {
	checking
	local code lag kind spent held cap percent over budgets
	code=$(admin_get /admin/status)
	if [ "$code" != 200 ]; then
		fail "/admin/status on the admin listener $(answered "$code")"
		return 0
	fi
	if ! jq -e 'type == "object"' "$BODY" >/dev/null 2>&1; then
		fail "/admin/status answered 200 without a status"
		return 0
	fi
	info "version $(jq -r '.version // "unknown"' "$BODY")"
	lag=$(jq -r '.lagBound // 0 | tostring' "$BODY")
	info "lagBound $lag characters"
	if ! [[ $lag =~ ^[0-9]+$ ]] || [ "$lag" -eq 0 ]; then
		fail "lagBound is $lag: aml-serve@$NAME does not run --consolidate background"
	fi
	budgets=$(jq -r --argjson ceiling "$BUDGET_CEILING" '(.budgets // [])[] |
		[.kind, .spent, .held, .cap,
		 (if .cap > 0 then ((.spent + .held) / .cap * 1000 | floor) / 10 else 0 end),
		 (.cap > 0 and (.spent + .held) / .cap >= $ceiling)] | map(tostring) | join(" ")' "$BODY") || budgets=
	if [ -z "$budgets" ]; then
		info "budgets: no paid account used yet"
		return 0
	fi
	while read -r kind spent held cap percent over; do
		info "budget $kind: spent $spent + held $held of cap $cap ($percent%)"
		[ "$over" != true ] || fail "budget $kind: spent and held are $percent% of the cap, not below 80%"
	done <<EOF
$budgets
EOF
}

check_smoke_user() {
	checking
	local key
	key=$(printf '%s' smoke-user | sha256_hex | cut -c 1-32)
	[ ! -e "$ROOT$STATE/$NAME/data/users/$key" ] ||
		fail "the data root holds the store of smoke-user (store ${key:0:8}), the runbook's smoke-test user: printf 'smoke-user\\n' | mnemon-instance delete-user $NAME"
}

# inspect_on: the instance's inspect service is on.
inspect_on() { [ -f "$ROOT$ETC/$NAME/inspect.env" ]; }

# check_inspect: only while the instance's inspect service is on: inspect.env
# is root's, mode 0600, and holds the names aml-inspect@.service reads and
# the provider keys the configuration names; /healthz on the inspect
# listener answers 200 with the inspect token.
check_inspect() {
	inspect_on || return 0
	checking
	local inspect="$ROOT$ETC/$NAME/inspect.env" owner mode keys var code
	read -r owner mode <<EOF
$(owner_mode "$inspect")
EOF
	[ "$owner" = "$(owner_uid)" ] || fail "$ETC/$NAME/inspect.env is owned by uid $owner, not root"
	[ "$mode" = 600 ] || fail "$ETC/$NAME/inspect.env has mode $mode, not 0600"
	keys=$(jq -r '[.. | objects | .keyEnv? | strings] | unique | .[]' "$ROOT$ETC/$NAME/memory-config.json" 2>/dev/null) || keys=
	for var in AML_INSPECT_LISTEN AML_INSPECT_DEBUG_CAPS MNEMON_INSPECT_TOKEN $keys; do
		has "$inspect" "$var" || fail "$ETC/$NAME/inspect.env lacks $var"
	done
	checking
	code=$(printf 'header = "Authorization: Bearer %s"\n' "$(env_get "$inspect" MNEMON_INSPECT_TOKEN)" |
		curl --disable --config - --silent --output /dev/null --max-time 10 --write-out '%{http_code}' \
			"http://$(env_get "$inspect" AML_INSPECT_LISTEN)/healthz") || true
	[ "${code:-000}" = 200 ] || fail "/healthz on the inspect listener $(answered "${code:-000}")"
}

check_units() {
	local unit state units="aml-serve@$NAME.service aml-watch@$NAME.timer"
	! inspect_on || units="$units aml-inspect@$NAME.service"
	for unit in $units caddy.service; do
		checking
		state=$(systemctl is-active "$unit" 2>/dev/null) || true
		[ "$state" = active ] || fail "$unit is ${state:-unknown}, not active"
	done
}

check_tools() {
	local out templates=("$UNITS/aml-serve@.service" "$UNITS/aml-watch@.service" "$UNITS/aml-watch@.timer")
	! inspect_on || templates+=("$UNITS/aml-inspect@.service")
	if command -v systemd-analyze >/dev/null 2>&1; then
		checking
		out=$(systemd-analyze verify "${templates[@]}" 2>&1) ||
			fail "systemd-analyze verify of the unit templates failed: $(printf '%s\n' "$out" | head -n 1)"
	fi
	if command -v caddy >/dev/null 2>&1; then
		checking
		caddy validate --config "$CADDYFILE" --adapter caddyfile >/dev/null 2>&1 ||
			fail "caddy validate --config $CADDYFILE failed: run it to see why"
	fi
}

check_wg0() {
	checking
	local ip link
	ip=$(env_get "$ROOT$ETC/$NAME/env" AML_ADMIN_LISTEN)
	ip=${ip%:*}
	if ! command -v ip >/dev/null 2>&1; then
		fail "ip is missing (apt install iproute2): cannot check wg0"
		return 0
	fi
	if ! link=$(ip -o link show dev wg0 2>/dev/null); then
		fail "wg0 does not exist: systemctl enable --now wg-quick@wg0"
		return 0
	fi
	case $link in
	*[\<,]UP[,\>]*) ;;
	*) fail "wg0 is down: systemctl start wg-quick@wg0" ;;
	esac
	ip -o -4 addr show dev wg0 2>/dev/null | grep -qF " $ip/" ||
		fail "wg0 does not carry $ip, the address of AML_ADMIN_LISTEN"
}

# open_to_anywhere RULE: RULE, a line of `ufw status verbose` with its spaces
# squeezed, lets anyone reach 51820/udp or an admin port (8091 to 8600, tcp):
# an ALLOW or LIMIT IN rule from Anywhere, over IPv4 or IPv6 ("(v6)"), on any
# interface ("on wg0"), to any address, whose ports (one, a range, a list, or
# all of them: "Anywhere") hold one of those. A trailing "# comment" is
# allowed. Rules for application profiles name no port, and pass.
open_to_anywhere() {
	local re='^(.+) (ALLOW|LIMIT) IN Anywhere( \(v6\))?( #.*)?$'
	local ports_re='^[0-9]{1,5}(:[0-9]{1,5})?(,[0-9]{1,5}(:[0-9]{1,5})?)*$'
	local rule=$1 to ports proto= item lo hi
	while [[ $rule == *' ' ]]; do
		rule=${rule% }
	done
	[[ $rule =~ $re ]] || return 1
	to=${BASH_REMATCH[1]}
	to=${to% on *}
	to=${to% (v6)}
	to=${to##* }
	[ "$to" != Anywhere ] || return 0
	case $to in
	*/tcp) proto=tcp ports=${to%/tcp} ;;
	*/udp) proto=udp ports=${to%/udp} ;;
	*) ports=$to ;;
	esac
	[[ $ports =~ $ports_re ]] || return 1
	local IFS=,
	for item in $ports; do
		lo=${item%%:*} hi=${item##*:}
		if [ "$proto" != udp ] && [ "$lo" -le 8600 ] && [ "$hi" -ge 8091 ]; then
			return 0
		fi
		if [ "$proto" != tcp ] && [ "$lo" -le 51820 ] && [ "$hi" -ge 51820 ]; then
			return 0
		fi
	done
	return 1
}

check_ufw() {
	checking
	local status rule
	if ! command -v ufw >/dev/null 2>&1; then
		fail "ufw is missing (apt install ufw)"
		return 0
	fi
	if ! status=$(ufw status verbose 2>&1); then
		fail "ufw status verbose failed"
		return 0
	fi
	status=$(printf '%s\n' "$status" | tr -s ' ')
	printf '%s\n' "$status" | grep -q '^Status: active$' || fail "ufw is not active: ufw enable, after the 22/tcp rule"
	printf '%s\n' "$status" | grep -q '^Default: deny (incoming)' || fail "ufw does not deny incoming by default: ufw default deny incoming"
	for rule in 22/tcp 80/tcp 443/tcp 443/udp; do
		printf '%s\n' "$status" | grep -Eq "^$rule ALLOW IN Anywhere( |\$)" || fail "ufw does not allow $rule: ufw allow $rule"
	done
	printf '%s\n' "$status" | grep -Eq '^51820/udp ALLOW IN [0-9]+\.[0-9]+\.[0-9]+\.[0-9]+( |$)' ||
		fail "ufw does not allow 51820/udp from the ops host: ufw allow from <ops address> to any port 51820 proto udp"
	printf '%s\n' "$status" | grep -Eq "^8091:8600/tcp on wg0 ALLOW IN ${HUB//./\\.}( |\$)" ||
		fail "ufw does not let the hub reach the admin ports: ufw allow in on wg0 from $HUB proto tcp to any port 8091:8600"
	while IFS= read -r rule; do
		if open_to_anywhere "$rule"; then
			fail "ufw allows 51820/udp or an admin port from anywhere: \"$rule\"; delete that rule (ufw status numbered, then ufw delete <number>)"
		fi
	done <<EOF
$status
EOF
}

main() {
	while [ $# -gt 0 ]; do
		case $1 in
		--root)
			[ $# -ge 2 ] || die "--root needs a directory"
			ROOT=${2%/}
			shift 2
			case $ROOT in
			/*) ;;
			*) die "--root must be an absolute path" ;;
			esac
			;;
		--no-systemd)
			SYSTEMD=0
			shift
			;;
		-h | --help)
			usage
			exit 0
			;;
		-*) die "unknown option $1" ;;
		*)
			[ -z "$NAME" ] || die "give one instance name"
			NAME=$1
			shift
			;;
		esac
	done
	local re='^[a-z][a-z0-9-]{1,30}$'
	[[ $NAME =~ $re ]] || die "give the name of an instance (2 to 31 lowercase letters, digits and hyphens): $PROG <n>"
	need curl jq grep tail cut tr head mktemp stat id
	command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1 || die "missing tools: sha256sum (apt install coreutils)"
	if [ -z "$ROOT" ] && [ "$(id -u)" != 0 ]; then
		die "run as root: the instance's env files are root's"
	fi
	[ -d "$ROOT$ETC/$NAME" ] || die "there is no instance $NAME on this host (mnemon-instance list)"
	BODY=$(mktemp)

	printf '%s %s\n' "$PROG" "$NAME"
	check_files
	check_engine_env
	check_watch_env
	check_health
	check_ready
	check_status
	check_smoke_user
	check_inspect
	if [ "$SYSTEMD" = 1 ]; then
		check_units
		check_tools
		check_wg0
		check_ufw
	fi
	if [ "$FAILED" -eq 0 ]; then
		printf '%s %s: all %d checks passed\n' "$PROG" "$NAME" "$CHECKED"
		exit 0
	fi
	local failures=failures
	[ "$FAILED" -ne 1 ] || failures=failure
	printf '%s %s: %d %s in %d checks\n' "$PROG" "$NAME" "$FAILED" "$failures" "$CHECKED"
	exit 1
}

main "$@"
