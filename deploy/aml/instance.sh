#!/bin/bash
# mnemon-instance: the engine instances of this host, one per account of the
# memory cloud. Install once per host as /usr/local/sbin/mnemon-instance and
# run it as root; see docs/aml-adapter.md ("Instances"). `mnemon-instance help`
# lists the commands.
#
# Instance <n> is these files, and the units aml-serve@<n>.service,
# aml-watch@<n>.service and aml-watch@<n>.timer:
#
#   /etc/mnemon-aml/<n>/env                engine environment: record, addresses, limits, secrets
#   /etc/mnemon-aml/<n>/watch.env          watcher environment: alert bot, public URL, admin token
#   /etc/mnemon-aml/<n>/memory-config.json the shipped configuration with this instance's caps
#   /var/lib/mnemon-aml/<n>/               data root (data/) and model budget file
#   /var/lib/mnemon-aml-watch/<n>/         the watcher's state
#   /etc/systemd/system/aml-serve@<n>.service.d/limits.conf   MemoryMax= and CPUQuota=
#   /etc/caddy/mnemon-instances/<n>.caddy  the route, on a shared host
#
# The env file is the instance's record (layout, slot, status); nothing else
# keeps state. Every file is written to a temporary file in its directory and
# renamed over the old one.
#
# Secrets (API keys, admin tokens, the provider keys and the alert webhook in
# /etc/mnemon-aml/host.env) never appear in an argument of a program, a URL or
# a log line. They move through files, pipes and shell builtins only, reach
# curl as a config on its standard input (--config -), and are printed only
# in the one-time output of new and rotate-key.
#
# For tests, --root DIR reads and writes every file under DIR (the files still
# name the host's paths), and --no-systemd runs neither systemctl, logger,
# curl, caddy nor mnemon-preflight, and writes the journal lines to
# DIR/var/log/mnemon-instance.log. A test may also source this file (main runs
# only when the file is run) to call its functions with stand-ins for those
# programs.
set -euo pipefail
umask 077
export LC_ALL=C

readonly PROG=mnemon-instance

# The host's paths. Under --root every file operation puts $ROOT in front.
readonly ETC=/etc/mnemon-aml
readonly HOST_ENV=/etc/mnemon-aml/host.env
readonly STATE=/var/lib/mnemon-aml
readonly WATCH_STATE=/var/lib/mnemon-aml-watch
readonly UNITS=/etc/systemd/system
readonly SNIPPETS=/etc/caddy/mnemon-instances
readonly CADDYFILE=/etc/caddy/Caddyfile
readonly SHIPPED_CONFIG=/usr/local/share/mnemon-aml/memory-config.industrial.json
readonly MNEMON=/usr/local/bin/mnemon
readonly PREFLIGHT=/usr/local/sbin/mnemon-preflight
readonly JOURNAL=/var/log/mnemon-instance.log

# Shared slots run 0 to 49: data port 8100+10s, admin port 8101+10s.
readonly SLOTS=50
# Seconds new, resume, rotate-key and delete-user wait for /ready.
readonly READY_WAIT=120
# Instance names that collide with a path of the kit or read as a common word.
readonly RESERVED_NAMES=" data env host host-env users watch instances aml mnemon caddy test-instance "

ROOT=
SYSTEMD=1
# Cleanup on exit: temporary files not yet renamed into place, what an
# unfinished new has created, the instance an unfinished resume has started,
# and the lock.
TEMPS=
CREATED=
RESUMING=
LOCKED=

# Defaults by layout, which new's flags override. The dedicated numbers are
# provisional: the load test on the evaluation host (Task 12 of the
# serve-hardening plan) replaces them with measured values.
dedicated_defaults() {
	ADDS=16 SEARCHES=16 STORES=16 WORKERS=4 MEMORY_MAX=40G CPU_QUOTA= DEEPSEEK_CAP=600 JEV_CAP=60
}
shared_defaults() {
	ADDS=4 SEARCHES=4 STORES=2 WORKERS=1 MEMORY_MAX=1G CPU_QUOTA=100% DEEPSEEK_CAP=30 JEV_CAP=3
}

usage() {
	cat <<EOF
Usage: $PROG [--root DIR] [--no-systemd] COMMAND ...

The engine instances of this host, one per account. Run as root.

  new <n> --layout dedicated|shared --public-url URL --admin-ip IP [options]
      Open instance <n>: write its files, start aml-serve@<n> and its watcher,
      wait for /ready, run mnemon-preflight, and print the base URL, the API
      key, the admin URL, the admin token and the registration JSON for the
      console. The keys are shown only then.
        --public-url URL            the base URL clients use: https://<domain>
                                    (dedicated) or https://<domain>/i/<n> (shared)
        --admin-ip IP               this host's WireGuard address (private IPv4)
        --host-name NAME            the host in the registration JSON (uname -n)
        --deepseek-cap CNY          DeepSeek cap: dedicated 600, shared 30
        --jev-cap USD               JEV cap: dedicated 60, shared 3
        --memory-max SIZE           MemoryMax=: dedicated 40G, shared 1G
        --cpu-quota PERCENT         CPUQuota=: dedicated none, shared 100%
        --max-concurrent-adds N     dedicated 16, shared 4
        --max-concurrent-searches N dedicated 16, shared 4
        --max-open-stores N         dedicated 16, shared 2
        --consolidation-workers N   dedicated 4, shared 1
  suspend <n>       Shared: answer 403 {"error":"account suspended"}. Then stop
                    and disable aml-serve@<n> and its watcher.
  resume <n>        Start aml-serve@<n> and wait for /ready; then restore the
                    route and start the watcher. If it is not ready, stop it
                    again: the instance stays suspended.
  rotate-key <n>    Write a new API key, restart, wait for /ready, and print
                    the new key and its fingerprint once.
  delete <n>        Ask for the name, then stop the instance and remove all its
                    files and data, printing each path removed.
  delete-user <n>   Ask for one user id (type it at the prompt; never put it
                    in a command) and remove that user's store, stopping and
                    starting the instance around it.
  list              Name, layout, slot, addresses, status and public URL of
                    every instance. No secrets.

For tests: --root DIR reads and writes every file under DIR; --no-systemd runs
no systemctl, logger, curl, caddy or mnemon-preflight, and writes the journal
lines to DIR$JOURNAL.
EOF
}

say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
die() {
	say "$*"
	exit 1
}
usage_error() {
	say "$*"
	printf "Run '%s help' for the commands.\n" "$PROG" >&2
	exit 2
}

on_exit() {
	local status=$? path
	for path in $TEMPS; do
		rm -f -- "$path"
	done
	if [ -n "$CREATED" ]; then
		for path in $CREATED; do
			rm -rf -- "$path"
		done
		say "new stopped before it finished; it removed what it had written"
		[ "$status" -ne 0 ] || status=1
	fi
	if [ -n "$RESUMING" ]; then
		if sctl disable --now "aml-serve@$RESUMING.service"; then
			say "resume $RESUMING did not finish: aml-serve@$RESUMING is stopped and disabled again, and $RESUMING stays suspended"
		else
			say "resume $RESUMING did not finish, and aml-serve@$RESUMING could not be stopped: systemctl disable --now aml-serve@$RESUMING"
		fi
		[ "$status" -ne 0 ] || status=1
	fi
	if [ -n "$LOCKED" ]; then
		rmdir -- "$LOCKED" 2>/dev/null || true
	fi
	exit "$status"
}
trap on_exit EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# need TOOL...: refuses to go on without each tool, naming its apt package.
need() {
	local tool missing=
	for tool in "$@"; do
		command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool (apt install $(package_of "$tool"))"
	done
	[ -z "$missing" ] || die "missing tools:$missing"
}

package_of() {
	case $1 in
	jq | curl) printf '%s' "$1" ;;
	systemctl) printf systemd ;;
	logger) printf bsdutils ;;
	shasum) printf perl ;;
	*) printf coreutils ;;
	esac
}

# host_checks: root on a host; the tools; the one-time install's directory.
host_checks() {
	if [ -z "$ROOT" ] && [ "$(id -u)" != 0 ]; then
		die "run as root: it writes /etc/mnemon-aml and runs systemctl"
	fi
	need jq grep tail cut head tr base64 mktemp mkdir chmod mv rm date uname id
	command -v sha256sum >/dev/null 2>&1 || need shasum
	if [ "$SYSTEMD" = 1 ]; then
		need systemctl logger curl sleep
	fi
	[ -d "$ROOT$ETC" ] || die "$ETC is missing: do the one-time host install first (docs/aml-adapter.md, \"Install once per host\")"
}

# sha256_hex: the SHA-256 of standard input, in hex.
sha256_hex() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum
	else
		shasum -a 256
	fi | cut -d ' ' -f 1
}

# fingerprint KEY: the first 12 hex digits of the SHA-256 of KEY.
fingerprint() { printf '%s' "$1" | sha256_hex | cut -c 1-12; }

# new_secret: mnm_ and 32 random bytes in base64url without padding. The
# operand "-_" comes after "--", or a strict tr would take it for options.
new_secret() {
	local raw
	raw=$(head -c 32 /dev/urandom | base64 | tr -- '+/' '-_' | tr -d '=\n')
	[ "${#raw}" -eq 43 ] || die "could not read 32 random bytes from /dev/urandom"
	printf 'mnm_%s' "$raw"
}

# env_get FILE NAME: the value of FILE's last NAME= line, "" without one. The
# file is read, never sourced; one pair of quotes around the value and
# trailing whitespace are dropped, as systemd drops them.
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

# owner_mode FILE: its owner's uid and its permission bits, as "0 600" (GNU
# or BSD stat).
owner_mode() {
	stat -c '%u %a' "$1" 2>/dev/null || stat -f '%u %Lp' "$1"
}

# owner_uid: the uid that owns the host's root-only files: root, or under
# --root the user running the tests.
owner_uid() {
	if [ -n "$ROOT" ]; then id -u; else printf 0; fi
}

# make_temp DIR: sets TMP to a new private (0600) file in DIR.
make_temp() {
	TMP=$(mktemp "$1/.mnemon-instance.XXXXXX")
	TEMPS="$TEMPS $TMP"
}

# commit TMP DEST MODE OWNER: gives TMP its mode and, on a host, its owner,
# and renames it over DEST.
commit() {
	chmod "$3" "$1"
	[ -n "$ROOT" ] || chown "$4" "$1"
	mv -f -- "$1" "$2"
}

# ensure_dir DIR MODE: DIR, made with its parents if missing (mode 0755) and
# given MODE when made. Systemd and Caddy read through these directories.
ensure_dir() {
	[ -d "$1" ] && return 0
	(umask 022 && mkdir -p -- "$1")
	chmod "$2" "$1"
}

# new_dir DIR MODE OWNER: a new directory of new's, which must not exist; it
# is removed again if new does not finish.
new_dir() {
	mkdir -- "$1"
	CREATED="$CREATED $1"
	chmod "$2" "$1"
	[ -n "$ROOT" ] || chown "$3" "$1"
}

# env_set FILE NAME VALUE: rewrites FILE with its NAME line set to VALUE
# (added if missing), atomically, root's and 0600.
env_set() {
	local file=$1 name=$2 value=$3 line found=0
	make_temp "${file%/*}"
	while IFS= read -r line || [ -n "$line" ]; do
		case $line in
		"$name="*)
			printf '%s=%s\n' "$name" "$value"
			found=1
			;;
		*) printf '%s\n' "$line" ;;
		esac
	done <"$file" >"$TMP"
	[ "$found" = 1 ] || printf '%s=%s\n' "$name" "$value" >>"$TMP"
	commit "$TMP" "$file" 0600 root:root
}

# journal MESSAGE: one line to the journal (logger -t mnemon-instance). It
# names the action and the instance, never a secret or a raw user id.
journal() {
	if [ "$SYSTEMD" = 1 ]; then
		logger -t "$PROG" -- "$1"
	else
		(umask 022 && mkdir -p -- "$ROOT${JOURNAL%/*}")
		printf '%s %s: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$PROG" "$1" >>"$ROOT$JOURNAL"
	fi
}

# sctl ARG...: systemctl, unless --no-systemd.
sctl() {
	[ "$SYSTEMD" = 0 ] || systemctl "$@"
}

# reload_caddy: checks the host's Caddyfile with the snippets as they are now
# (when caddy is installed here), then reloads Caddy.
reload_caddy() {
	[ "$SYSTEMD" = 1 ] || return 0
	if command -v caddy >/dev/null 2>&1; then
		if ! caddy validate --config "$CADDYFILE" --adapter caddyfile >/dev/null 2>&1; then
			say "caddy validate refused $CADDYFILE; run it by hand to see why"
			return 1
		fi
	fi
	systemctl reload caddy
}

# admin_status NAME PATH: the HTTP status the instance's admin listener
# answers for PATH, with the admin token, "000" without an answer.
admin_status() {
	local env="$ROOT$ETC/$1/env" admin code
	admin=$(env_get "$env" AML_ADMIN_LISTEN)
	code=$(printf 'header = "Authorization: Bearer %s"\n' "$(env_get "$env" MNEMON_ADMIN_TOKEN)" |
		curl --disable --config - --silent --output /dev/null --max-time 5 --write-out '%{http_code}' "http://$admin$2") || true
	printf '%s' "${code:-000}"
}

# wait_ready NAME: waits for the instance's /ready to answer 200, for
# READY_WAIT seconds at most however long each try takes (curl gives up on an
# address that does not answer after 5 s).
wait_ready() {
	[ "$SYSTEMD" = 1 ] || return 0
	local deadline=$((SECONDS + READY_WAIT))
	while :; do
		[ "$(admin_status "$1" /ready)" != 200 ] || return 0
		[ "$SECONDS" -lt "$deadline" ] || break
		sleep 1
	done
	say "$1: /ready did not answer 200 within ${READY_WAIT}s; see journalctl -u aml-serve@$1 and mnemon-preflight $1"
	return 1
}

# lock: one mnemon-instance at a time changes the host's instances.
lock() {
	local dir="$ROOT$ETC/.mnemon-instance.lock"
	mkdir -- "$dir" 2>/dev/null ||
		die "another $PROG is running (it holds $ETC/.mnemon-instance.lock); if none is, remove that directory"
	LOCKED=$dir
}

valid_name() {
	local re='^[a-z][a-z0-9-]{1,30}$'
	[[ $1 =~ $re ]]
}

# check_name NAME: NAME is a valid instance name and not a reserved one. Every
# subcommand calls it, through require_instance or directly, before it builds
# a path from the name.
check_name() {
	valid_name "$1" || usage_error "instance names are 2 to 31 lowercase letters, digits and hyphens, starting with a letter: not \"$1\""
	case $RESERVED_NAMES in
	*" $1 "*) usage_error "the instance name \"$1\" is reserved: it collides with a path of the kit or reads as a common word; choose another" ;;
	esac
}

# instance_paths NAME: the host paths an instance owns.
instance_paths() {
	printf '%s\n' "$ETC/$1" "$STATE/$1" "$WATCH_STATE/$1" "$UNITS/aml-serve@$1.service.d" "$SNIPPETS/$1.caddy"
}

# require_instance NAME: the instance exists (its env file does).
require_instance() {
	check_name "$1"
	[ -f "$ROOT$ETC/$1/env" ] || die "there is no instance $1 on this host ($PROG list)"
}

# private_ipv4 IP: a dotted IPv4 address in 10/8, 172.16/12 or 192.168/16.
private_ipv4() {
	local re='^(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})$'
	[[ $1 =~ $re ]] || return 1
	local a=${BASH_REMATCH[1]} b=${BASH_REMATCH[2]} c=${BASH_REMATCH[3]} d=${BASH_REMATCH[4]}
	((a <= 255 && b <= 255 && c <= 255 && d <= 255)) || return 1
	((a == 10 || (a == 172 && b >= 16 && b <= 31) || (a == 192 && b == 168)))
}

# gomemlimit_mib SIZE: GOMEMLIMIT, in MiB, for a MemoryMax= of SIZE (a number
# of at most 10 digits and K, M, G or T, in powers of 1024 as systemd reads
# them): 80% of it, rounded down to a whole MiB. 40G gives 32768, 1G 819.
gomemlimit_mib() {
	local n=${1%?}
	case $1 in
	*K) printf '%s' $((n * 8 / 10240)) ;;
	*M) printf '%s' $((n * 8 / 10)) ;;
	*G) printf '%s' $((n * 1024 * 8 / 10)) ;;
	*T) printf '%s' $((n * 1048576 * 8 / 10)) ;;
	esac
}

# config_keys FILE: the environment variables FILE's models read their keys
# from (keyEnv), one per line.
config_keys() {
	jq -r '[.. | objects | .keyEnv? | strings] | unique | .[]' "$1"
}

# check_host_env KEYS: host.env is root's, mode 0600, and holds the provider
# keys KEYS names and the alert bot. A refusal names the variable, never its
# value.
check_host_env() {
	local file="$ROOT$HOST_ENV" owner mode var
	[ -f "$file" ] || die "$HOST_ENV is missing: it holds the host's provider keys and alert bot (docs/aml-adapter.md, \"Install once per host\")"
	read -r owner mode <<EOF
$(owner_mode "$file")
EOF
	[ "$owner" = "$(owner_uid)" ] || die "$HOST_ENV must be owned by root (it is owned by uid $owner)"
	[ "$mode" = 600 ] || die "$HOST_ENV must have mode 0600 (it has $mode)"
	for var in $1; do
		[ -n "$(env_get "$file" "$var")" ] || die "$HOST_ENV lacks $var, which the shipped configuration needs"
	done
	for var in AML_ALERT_WEBHOOK AML_ALERT_KIND; do
		[ -n "$(env_get "$file" "$var")" ] || die "$HOST_ENV lacks $var, which the watcher needs"
	done
	case $(env_get "$file" AML_ALERT_KIND) in
	telegram | feishu | wecom) ;;
	*) die "$HOST_ENV: AML_ALERT_KIND must be telegram, feishu or wecom" ;;
	esac
	case $(env_get "$file" AML_ALERT_WEBHOOK) in
	https://?*) ;;
	*) die "$HOST_ENV: AML_ALERT_WEBHOOK must be an https URL" ;;
	esac
	if [ "$(env_get "$file" AML_ALERT_KIND)" = telegram ] && ! telegram_url "$(env_get "$file" AML_ALERT_WEBHOOK)"; then
		die "$HOST_ENV: AML_ALERT_WEBHOOK must be the Telegram bot's sendMessage URL with its chat: https://api.telegram.org/bot<token>/sendMessage?chat_id=<chat id>"
	fi
}

# telegram_url URL: URL is a Telegram bot's sendMessage with its chat, as the
# watcher requires: https://<host>/bot<token>/sendMessage?chat_id=<chat id>,
# chat_id its only parameter, a chat's number (negative for a group or a
# channel) or a channel's @username. The value never leaves the shell.
telegram_url() {
	local re='^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?/([^?#%[:space:]]*/)?sendMessage\?chat_id=(-?[0-9]{1,20}|(@|%40)[A-Za-z][A-Za-z0-9_]{4,31})$'
	[[ $1 =~ $re ]]
}

# host_line NAME: host.env's NAME line as it is written, for an instance file.
host_line() {
	grep -E "^$1=" "$ROOT$HOST_ENV" | tail -n 1
}

snippet_active() { # NAME SLOT PORT
	cat <<EOF
# Instance $1, slot $2: its route on this shared host, written by $PROG.
# suspend and resume replace this file; do not edit it.
handle_path /i/$1/* {
	reverse_proxy 127.0.0.1:$3 {
		# Through a restart of aml-serve@$1, Caddy holds a request and dials
		# again every 250 ms, for up to 30 s. It retries a POST only when its
		# dial failed: such a request was never sent, so a POST is never sent
		# twice. (A GET, such as /health, may also be retried after other
		# errors.)
		lb_try_duration 30s
		lb_try_interval 250ms

		# The platform waits up to 31 minutes for an Add or a Search.
		transport http {
			keepalive 60s
			dial_timeout 5s
			response_header_timeout 35m
			read_timeout 35m
			write_timeout 35m
		}
	}
}
EOF
}

snippet_suspended() { # NAME SLOT
	cat <<EOF
# Instance $1, slot $2: suspended by $PROG, which answers for it in the
# engine's error shape. resume restores the route; do not edit this file.
handle_path /i/$1/* {
	header Content-Type application/json
	respond \`{"error":"account suspended"}\` 403
}
EOF
}

# write_snippet NAME active|suspended: the instance's route, atomically.
write_snippet() {
	local env="$ROOT$ETC/$1/env" slot port
	slot=$(env_get "$env" AML_SLOT)
	port=$(env_get "$env" AML_LISTEN)
	port=${port##*:}
	ensure_dir "$ROOT$SNIPPETS" 0755
	make_temp "$ROOT$SNIPPETS"
	if [ "$2" = active ]; then
		snippet_active "$1" "$slot" "$port" >"$TMP"
	else
		snippet_suspended "$1" "$slot" >"$TMP"
	fi
	commit "$TMP" "$ROOT$SNIPPETS/$1.caddy" 0644 root:root
}

# set_route NAME active|suspended: the route on a shared host, checked by
# Caddy and reloaded. If Caddy refuses it, the route is put back as it was:
# the old snippet, or none when there was none, so that the host's Caddyfile
# stays valid.
set_route() {
	local env="$ROOT$ETC/$1/env" snippet="$ROOT$SNIPPETS/$1.caddy" old= had=0
	[ "$(env_get "$env" AML_LAYOUT)" = shared ] || return 0
	if [ -f "$snippet" ]; then
		old=$(cat -- "$snippet")
		had=1
	fi
	write_snippet "$1" "$2"
	reload_caddy && return 0
	if [ "$had" = 1 ]; then
		make_temp "$ROOT$SNIPPETS"
		printf '%s\n' "$old" >"$TMP"
		commit "$TMP" "$snippet" 0644 root:root
		die "Caddy did not take the $2 route of $1; its snippet is as it was"
	fi
	rm -f -- "$snippet"
	die "Caddy did not take the $2 route of $1, which had no snippet; the new one is removed, so it still has none"
}

cmd_new() {
	local name= layout= public_url= admin_ip= host_name= opt value
	local ADDS SEARCHES STORES WORKERS MEMORY_MAX CPU_QUOTA DEEPSEEK_CAP JEV_CAP
	local adds= searches= stores= workers= memory_max= cpu_quota= deepseek_cap= jev_cap=
	while [ $# -gt 0 ]; do
		case $1 in
		--*=*) set -- "${1%%=*}" "${1#*=}" "${@:2}" ;;
		esac
		case $1 in
		--layout | --public-url | --admin-ip | --host-name | --deepseek-cap | --jev-cap | --memory-max | --cpu-quota | \
			--max-concurrent-adds | --max-concurrent-searches | --max-open-stores | --consolidation-workers)
			[ $# -ge 2 ] || usage_error "new: $1 needs a value"
			opt=$1 value=$2
			shift 2
			case $opt in
			--layout) layout=$value ;;
			--public-url) public_url=$value ;;
			--admin-ip) admin_ip=$value ;;
			--host-name) host_name=$value ;;
			--deepseek-cap) deepseek_cap=$value ;;
			--jev-cap) jev_cap=$value ;;
			--memory-max) memory_max=$value ;;
			--cpu-quota) cpu_quota=$value ;;
			--max-concurrent-adds) adds=$value ;;
			--max-concurrent-searches) searches=$value ;;
			--max-open-stores) stores=$value ;;
			--consolidation-workers) workers=$value ;;
			esac
			;;
		-*) usage_error "new: unknown flag $1" ;;
		*)
			[ -z "$name" ] || usage_error "new: one instance name, not \"$name\" and \"$1\""
			name=$1
			shift
			;;
		esac
	done
	check_name "$name"
	case $layout in
	dedicated) dedicated_defaults ;;
	shared) shared_defaults ;;
	*) usage_error "new: --layout must be dedicated or shared" ;;
	esac
	ADDS=${adds:-$ADDS} SEARCHES=${searches:-$SEARCHES} STORES=${stores:-$STORES} WORKERS=${workers:-$WORKERS}
	MEMORY_MAX=${memory_max:-$MEMORY_MAX} CPU_QUOTA=${cpu_quota:-$CPU_QUOTA}
	DEEPSEEK_CAP=${deepseek_cap:-$DEEPSEEK_CAP} JEV_CAP=${jev_cap:-$JEV_CAP}
	local count='^(0|[1-9][0-9]*)$' positive='^[1-9][0-9]*$' cap='^(0|[1-9][0-9]*)(\.[0-9]+)?$' zero='^0(\.0+)?$'
	[[ $ADDS =~ $count ]] || usage_error "new: --max-concurrent-adds must be a whole number (0 for no limit)"
	[[ $SEARCHES =~ $count ]] || usage_error "new: --max-concurrent-searches must be a whole number (0 for no limit)"
	[[ $STORES =~ $positive ]] || usage_error "new: --max-open-stores must be a whole number of at least 1"
	[[ $WORKERS =~ $positive ]] || usage_error "new: --consolidation-workers must be a whole number of at least 1"
	local size='^[1-9][0-9]{0,9}[KMGT]$' percent='^[1-9][0-9]*%$' gomemlimit
	[[ $MEMORY_MAX =~ $size ]] || usage_error "new: --memory-max must be a size such as 1G or 1536M"
	# The Go runtime collects harder near this soft limit, below the hard one.
	gomemlimit=$(gomemlimit_mib "$MEMORY_MAX")
	[ "$gomemlimit" -ge 1 ] || usage_error "new: --memory-max $MEMORY_MAX is too small: GOMEMLIMIT, 80% of it, would be under 1 MiB"
	[ -z "$CPU_QUOTA" ] || [[ $CPU_QUOTA =~ $percent ]] || usage_error "new: --cpu-quota must be a percentage such as 100%"
	for value in "$DEEPSEEK_CAP" "$JEV_CAP"; do
		[[ $value =~ $cap ]] && ! [[ $value =~ $zero ]] || usage_error "new: --deepseek-cap and --jev-cap must be positive numbers, such as 30 or 2.5"
	done
	private_ipv4 "$admin_ip" ||
		usage_error "new: --admin-ip must be this host's WireGuard address, a private IPv4 address such as 10.77.0.3"

	# The public URL is https, without credentials, query or fragment. A
	# dedicated instance is at the domain's root; a shared one at /i/<n>.
	public_url=${public_url%/}
	local authority path host_re='^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$'
	case $public_url in
	https://*) ;;
	*) usage_error "new: --public-url must be the https URL clients use" ;;
	esac
	authority=${public_url#https://}
	authority=${authority%%/*}
	path=${public_url#https://"$authority"}
	[[ $authority =~ $host_re ]] || usage_error "new: --public-url must be https://<domain>[/i/<n>], not \"$public_url\""
	if [ "$layout" = dedicated ] && [ -n "$path" ]; then
		usage_error "new: a dedicated instance is at its domain's root: --public-url https://$authority"
	fi
	if [ "$layout" = shared ] && [ "$path" != "/i/$name" ]; then
		usage_error "new: a shared instance is at /i/<n> of the host's domain: --public-url https://$authority/i/$name"
	fi

	[ -n "$host_name" ] || host_name=$(uname -n)
	local host_name_re='^[A-Za-z0-9][A-Za-z0-9.-]{0,62}$'
	[[ $host_name =~ $host_name_re ]] || usage_error "new: the host name \"$host_name\" is not a plain host name: give --host-name"

	host_checks
	[ -f "$ROOT$SHIPPED_CONFIG" ] || die "$SHIPPED_CONFIG is missing: install the shipped configuration (docs/aml-adapter.md, \"Install once per host\")"
	local keys key_re='^[A-Z_][A-Z0-9_]*$' var
	keys=$(config_keys "$ROOT$SHIPPED_CONFIG") || die "$SHIPPED_CONFIG is not readable JSON"
	for var in $keys; do
		[[ $var =~ $key_re ]] || die "$SHIPPED_CONFIG names the key variable \"$var\", which is not an environment variable name"
	done
	check_host_env "$keys"
	if [ "$layout" = shared ]; then
		grep -qF "import $SNIPPETS/*.caddy" "$ROOT$CADDYFILE" 2>/dev/null ||
			die "$CADDYFILE does not import $SNIPPETS/*.caddy: install Caddyfile.shared on this host first"
	elif ! grep -qF 'reverse_proxy 127.0.0.1:8090' "$ROOT$CADDYFILE" 2>/dev/null; then
		say "warning: $CADDYFILE does not proxy to 127.0.0.1:8090; install Caddyfile.dedicated with the domain"
	fi
	if [ "$SYSTEMD" = 1 ]; then
		[ -x "$ROOT$MNEMON" ] || die "$MNEMON is missing: install the binary first"
		for var in aml-serve@.service aml-watch@.service aml-watch@.timer; do
			[ -f "$ROOT$UNITS/$var" ] || die "$UNITS/$var is missing: install the unit templates first"
		done
		id mnemon >/dev/null 2>&1 || die "the user mnemon is missing: create it first (useradd --system mnemon)"
	fi

	lock
	local existing env other slot=
	while IFS= read -r existing; do
		[ ! -e "$ROOT$existing" ] || die "instance $name exists already ($existing); delete it first"
	done <<EOF
$(instance_paths "$name")
EOF
	local used=" "
	for env in "$ROOT$ETC"/*/env; do
		[ -f "$env" ] || continue
		other=${env%/env}
		other=${other##*/}
		if [ "$layout" = dedicated ]; then
			die "this host already has instance $other: a dedicated host holds one instance"
		fi
		if [ "$(env_get "$env" AML_LAYOUT)" = dedicated ]; then
			die "this host is dedicated to instance $other"
		fi
		used="$used$(env_get "$env" AML_SLOT) "
	done
	local data_port admin_port
	if [ "$layout" = dedicated ]; then
		slot=0 data_port=8090 admin_port=8091
	else
		local s
		for ((s = 0; s < SLOTS; s++)); do
			case $used in
			*" $s "*) ;;
			*)
				slot=$s
				break
				;;
			esac
		done
		[ -n "$slot" ] || die "all $SLOTS shared slots of this host are taken"
		data_port=$((8100 + 10 * slot)) admin_port=$((8101 + 10 * slot))
	fi
	local listen="127.0.0.1:$data_port" admin_listen="$admin_ip:$admin_port"
	local api_key admin_token
	api_key=$(new_secret)
	admin_token=$(new_secret)

	# Write the instance; if anything fails, on_exit removes what was made.
	local dir="$ROOT$ETC/$name" dropin="$ROOT$UNITS/aml-serve@$name.service.d"
	new_dir "$dir" 0750 root:mnemon
	make_temp "$dir"
	{
		printf '# aml-serve@%s: written by %s. Root-only (0600): it holds the API\n' "$name" "$PROG"
		printf '# key, the admin token and the provider keys. systemd reads it as root.\n'
		printf '# The record of the instance: its layout, slot and status.\n'
		printf 'AML_LAYOUT=%s\nAML_SLOT=%s\nAML_STATUS=active\n' "$layout" "$slot"
		printf '# The listeners: data on loopback behind Caddy, admin on the WireGuard address.\n'
		printf 'AML_LISTEN=%s\nAML_ADMIN_LISTEN=%s\n' "$listen" "$admin_listen"
		printf '# The flag values of aml-serve@.service.\n'
		printf 'AML_MAX_ADDS=%s\nAML_MAX_SEARCHES=%s\nAML_MAX_OPEN_STORES=%s\nAML_WORKERS=%s\n' "$ADDS" "$SEARCHES" "$STORES" "$WORKERS"
		printf '# The soft memory limit of the Go runtime: 80%% of MemoryMax=%s (limits.conf).\n' "$MEMORY_MAX"
		printf 'GOMEMLIMIT=%sMiB\n' "$gomemlimit"
		printf '# Secrets: the data-plane API key, the admin token, the provider keys.\n'
		printf 'AML_API_KEY=%s\nMNEMON_ADMIN_TOKEN=%s\n' "$api_key" "$admin_token"
		for var in $keys; do
			host_line "$var"
		done
	} >"$TMP"
	commit "$TMP" "$dir/env" 0600 root:root
	make_temp "$dir"
	{
		printf '# aml-watch@%s: written by %s. Root-only (0600): it holds the alert\n' "$name" "$PROG"
		printf '# webhook and the admin token, and no other secret.\n'
		host_line AML_ALERT_WEBHOOK
		host_line AML_ALERT_KIND
		printf 'AML_PUBLIC_URL=%s\nAML_ADMIN_LISTEN=%s\nMNEMON_ADMIN_TOKEN=%s\n' "$public_url" "$admin_listen" "$admin_token"
	} >"$TMP"
	commit "$TMP" "$dir/watch.env" 0600 root:root
	make_temp "$dir"
	jq --argjson deepseek "$DEEPSEEK_CAP" --argjson jev "$JEV_CAP" --arg budget "$STATE/$name/model-budget.db" \
		'.budgetFile = $budget | .budgets.jev = $jev | .budgets.deepseek = $deepseek' "$ROOT$SHIPPED_CONFIG" >"$TMP"
	commit "$TMP" "$dir/memory-config.json" 0640 root:mnemon

	ensure_dir "$ROOT$STATE" 0755
	new_dir "$ROOT$STATE/$name" 0700 mnemon:mnemon
	ensure_dir "$ROOT$WATCH_STATE" 0755
	new_dir "$ROOT$WATCH_STATE/$name" 0700 mnemon:mnemon

	ensure_dir "$ROOT$UNITS" 0755
	new_dir "$dropin" 0755 root:root
	make_temp "$dropin"
	{
		printf '# aml-serve@%s: its resource limits, written by %s new.\n' "$name" "$PROG"
		if [ "$layout" = dedicated ]; then
			printf '# Provisional: the load test on the evaluation host (Task 12 of the\n'
			printf '# serve-hardening plan) replaces these numbers with measured values.\n'
		fi
		printf '[Service]\nMemoryMax=%s\n' "$MEMORY_MAX"
		[ -z "$CPU_QUOTA" ] || printf 'CPUQuota=%s\n' "$CPU_QUOTA"
	} >"$TMP"
	commit "$TMP" "$dropin/limits.conf" 0644 root:root

	if [ "$layout" = shared ]; then
		write_snippet "$name" active
		CREATED="$CREATED $ROOT$SNIPPETS/$name.caddy"
	fi
	CREATED=
	# The keys are printed below whatever happens from here on: a journal line
	# that cannot be written is said, not fatal.
	journal "new $name: $layout, slot $slot, data $listen, admin $admin_listen, API key fingerprint $(fingerprint "$api_key")" ||
		say "could not write the journal line"

	# Start it. A failure from here on leaves the instance in place: it is
	# reported, and the keys are printed all the same.
	local started=1 checked=1
	if [ "$SYSTEMD" = 1 ]; then
		say "starting aml-serve@$name and its watcher"
		if sctl daemon-reload && sctl enable --now "aml-serve@$name.service" && wait_ready "$name"; then
			sctl enable --now "aml-watch@$name.timer" || started=0
			# A route Caddy refuses leaves no snippet behind (there was none
			# before), so that the host's Caddyfile stays valid.
			if [ "$layout" = shared ] && ! reload_caddy; then
				rm -f -- "$ROOT$SNIPPETS/$name.caddy"
				say "Caddy did not take the route of $name; its snippet is removed, so that the host's Caddyfile stays valid"
				started=0
			fi
		else
			started=0
		fi
		if [ "$started" = 1 ]; then
			if [ -x "$PREFLIGHT" ]; then
				"$PREFLIGHT" "$name" || checked=0
			else
				say "$PREFLIGHT is not installed: no preflight was run"
				checked=0
			fi
		fi
	fi

	local admin_url="http://$admin_listen"
	printf '\nInstance %s (%s, slot %s)\n\n' "$name" "$layout" "$slot"
	printf 'Base URL:     %s\n' "$public_url"
	printf 'API key:      %s\n' "$api_key"
	printf 'Admin URL:    %s\n' "$admin_url"
	printf 'Admin token:  %s\n\n' "$admin_token"
	printf 'Registration JSON for the console (it holds the admin token):\n'
	printf '{"id":"%s","kind":"%s",\n "host":{"name":"%s","vpcIp":"%s"},\n "publicUrl":"%s",\n "adminUrl":"%s",\n "adminToken":"%s","apiKeyFingerprint":"%s"}\n\n' \
		"$name" "$layout" "$host_name" "$admin_ip" "$public_url" "$admin_url" "$admin_token" "$(fingerprint "$api_key")"
	printf 'The API key and the admin token are shown only now. Hand the base URL and\n'
	printf 'the API key to the account holder over a private channel.\n'
	[ "$started" = 1 ] || die "instance $name is written but did not start fully (see above): see journalctl -u aml-serve@$name, then mnemon-preflight $name; $PROG delete $name removes it"
	[ "$checked" = 1 ] || die "instance $name runs, but its preflight did not pass"
}

cmd_suspend() {
	[ $# -eq 1 ] || usage_error "suspend: give the instance name"
	require_instance "$1"
	local name=$1 env="$ROOT$ETC/$1/env"
	host_checks
	lock
	set_route "$name" suspended
	sctl disable --now "aml-watch@$name.timer"
	sctl stop "aml-watch@$name.service"
	sctl disable --now "aml-serve@$name.service"
	env_set "$env" AML_STATUS suspended
	journal "suspend $name"
	printf '%s is suspended: aml-serve@%s and its watcher are stopped and disabled' "$name" "$name"
	if [ "$(env_get "$env" AML_LAYOUT)" = shared ]; then
		printf ', and its route answers 403'
	fi
	printf '.\n'
}

# cmd_resume NAME: starts aml-serve@NAME and waits for /ready; only then does
# it restore the route, record AML_STATUS=active and enable the watcher's
# timer. For a suspended instance, until the route is back, any failure (or a
# signal) makes on_exit stop and disable aml-serve@NAME again: the instance
# stays fully suspended. An active one is left running whatever happens.
cmd_resume() {
	[ $# -eq 1 ] || usage_error "resume: give the instance name"
	require_instance "$1"
	local name=$1 env="$ROOT$ETC/$1/env"
	host_checks
	lock
	[ "$(env_get "$env" AML_STATUS)" != suspended ] || RESUMING=$name
	sctl enable --now "aml-serve@$name.service"
	wait_ready "$name" || die "resume $name: the instance did not become ready"
	set_route "$name" active
	RESUMING=
	env_set "$env" AML_STATUS active
	sctl enable --now "aml-watch@$name.timer"
	journal "resume $name"
	printf '%s is running again.\n' "$name"
}

cmd_rotate_key() {
	[ $# -eq 1 ] || usage_error "rotate-key: give the instance name"
	require_instance "$1"
	local name=$1 env="$ROOT$ETC/$1/env" key ready=1
	host_checks
	lock
	key=$(new_secret)
	env_set "$env" AML_API_KEY "$key"
	# The new key is printed below whatever happens from here on.
	journal "rotate-key $name: new API key, fingerprint $(fingerprint "$key")" ||
		say "could not write the journal line"
	if [ "$(env_get "$env" AML_STATUS)" = suspended ]; then
		say "$name is suspended: the new key applies when it resumes"
	elif ! sctl restart "aml-serve@$name.service" || ! wait_ready "$name"; then
		ready=0
	fi
	printf 'New API key:  %s\n' "$key"
	printf 'Fingerprint:  %s\n\n' "$(fingerprint "$key")"
	printf 'The key is shown only now. Hand it to the account holder over a private\n'
	printf 'channel, and set the fingerprint in the console.\n'
	[ "$ready" = 1 ] || die "rotate-key $name: the key is written, but the instance is not ready"
}

cmd_delete() {
	[ $# -eq 1 ] || usage_error "delete: give the instance name"
	local name=$1 path found= typed=
	check_name "$name"
	while IFS= read -r path; do
		[ ! -e "$ROOT$path" ] || found=1
	done <<EOF
$(instance_paths "$name")
EOF
	[ -n "$found" ] || die "there is no instance $name on this host ($PROG list)"
	host_checks
	printf 'This removes instance %s for good: its keys, every user'"'"'s data and its\n' "$name" >&2
	printf 'model budget. Type the name of the instance to delete it: ' >&2
	IFS= read -r typed || true
	[ -t 0 ] || printf '\n' >&2
	[ "$typed" = "$name" ] || die "delete $name: the name typed does not match; nothing was deleted"
	lock
	local layout
	layout=$(env_get "$ROOT$ETC/$name/env" AML_LAYOUT)
	sctl disable --now "aml-watch@$name.timer"
	sctl stop "aml-watch@$name.service"
	sctl disable --now "aml-serve@$name.service"
	if [ -e "$ROOT$SNIPPETS/$name.caddy" ]; then
		rm -f -- "$ROOT$SNIPPETS/$name.caddy"
		reload_caddy || say "warning: Caddy did not reload; run: systemctl reload caddy"
		printf 'removed %s\n' "$SNIPPETS/$name.caddy"
	fi
	for path in "$ETC/$name" "$STATE/$name" "$WATCH_STATE/$name" "$UNITS/aml-serve@$name.service.d"; do
		if [ -e "$ROOT$path" ]; then
			rm -rf -- "$ROOT$path"
			printf 'removed %s\n' "$path"
		fi
	done
	sctl daemon-reload
	sctl reset-failed "aml-serve@$name.service" "aml-watch@$name.service" "aml-watch@$name.timer" 2>/dev/null || true
	journal "delete $name"
	if [ "$layout" = dedicated ]; then
		printf '%s still proxies to 127.0.0.1:8090, which answers 502 until the next dedicated instance runs.\n' "$CADDYFILE"
	fi
	printf 'Mark %s deleted in the console.\n' "$name"
}

cmd_delete_user() {
	[ $# -eq 1 ] || usage_error "delete-user: give the instance name; type the user id at its prompt"
	require_instance "$1"
	local name=$1 env="$ROOT$ETC/$1/env" user= key store active=1 ready=1
	host_checks
	[ ! -t 0 ] || printf 'User id: ' >&2
	IFS= read -r user || true
	[ -n "$user" ] || die "delete-user $name: no user id: run $PROG delete-user $name and type the id at its prompt, never in a command"
	key=$(printf '%s' "$user" | sha256_hex | cut -c 1-32)
	user=
	# The key names the directory removed below: it must be the 32 hex
	# digits of a store, whatever the SHA-256 tool printed.
	[[ $key =~ ^[0-9a-f]{32}$ ]] || die "delete-user $name: the SHA-256 tool gave no digest, so the user's store is unknown; nothing was deleted"
	store="$ROOT$STATE/$name/data/users/$key"
	[ -d "$store" ] || die "delete-user $name: there is no store for that user (store ${key:0:8}); nothing was deleted"
	lock
	[ "$(env_get "$env" AML_STATUS)" != suspended ] || active=0
	if [ "$active" = 1 ]; then
		sctl stop "aml-serve@$name.service"
	fi
	rm -rf -- "$store"
	if [ "$active" = 1 ]; then
		sctl start "aml-serve@$name.service" && wait_ready "$name" || ready=0
	fi
	journal "delete-user $name: removed store ${key:0:8}"
	printf 'Removed the store %s of %s.\n' "${key:0:8}" "$name"
	[ "$ready" = 1 ] || die "delete-user $name: the store is removed, but the instance is not ready"
}

cmd_list() {
	[ $# -eq 0 ] || usage_error "list takes no arguments"
	if [ -z "$ROOT" ] && [ "$(id -u)" != 0 ]; then
		die "run as root: the environment files are root's"
	fi
	need grep tail
	local env dir format='%-31s  %-9s  %4s  %-15s  %-21s  %-9s  %s\n'
	# shellcheck disable=SC2059
	printf "$format" NAME LAYOUT SLOT DATA ADMIN STATUS 'PUBLIC URL'
	for env in "$ROOT$ETC"/*/env; do
		[ -f "$env" ] || continue
		dir=${env%/env}
		# shellcheck disable=SC2059
		printf "$format" "${dir##*/}" "$(env_get "$env" AML_LAYOUT)" "$(env_get "$env" AML_SLOT)" \
			"$(env_get "$env" AML_LISTEN)" "$(env_get "$env" AML_ADMIN_LISTEN)" "$(env_get "$env" AML_STATUS)" \
			"$(env_get "$dir/watch.env" AML_PUBLIC_URL)"
	done
}

main() {
	while [ $# -gt 0 ]; do
		case $1 in
		--root)
			[ $# -ge 2 ] || usage_error "--root needs a directory"
			ROOT=${2%/}
			shift 2
			case $ROOT in
			/*) ;;
			*) usage_error "--root must be an absolute path" ;;
			esac
			case $ROOT in
			*[!A-Za-z0-9._/@+-]*) usage_error "--root must be a plain path (letters, digits and ._/@+-)" ;;
			esac
			[ -d "$ROOT" ] || usage_error "--root $ROOT is not a directory"
			;;
		--no-systemd)
			SYSTEMD=0
			shift
			;;
		-h | --help)
			usage
			exit 0
			;;
		-*) usage_error "unknown option $1" ;;
		*) break ;;
		esac
	done
	[ $# -gt 0 ] || usage_error "give a command"
	[ -z "$ROOT" ] || [ "$SYSTEMD" = 0 ] || usage_error "--root is for tests and needs --no-systemd"
	local command=$1
	shift
	case $command in
	new) cmd_new "$@" ;;
	suspend) cmd_suspend "$@" ;;
	resume) cmd_resume "$@" ;;
	rotate-key) cmd_rotate_key "$@" ;;
	delete) cmd_delete "$@" ;;
	delete-user) cmd_delete_user "$@" ;;
	list) cmd_list "$@" ;;
	help) usage ;;
	*) usage_error "unknown command $command" ;;
	esac
}

# Run as a program (read from a file or, without BASH_SOURCE, from standard
# input). A test sources the file instead, to call its functions.
if [ "${BASH_SOURCE[0]-$0}" = "$0" ]; then
	main "$@"
fi
