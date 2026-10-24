#!/bin/bash
# scripts/aml/final.sh: the final verification run in one command: the whole
# fault drill and the load test of one commit, on a quiet machine.
#
#   scripts/aml/final.sh [--rev REV] [--out DIR] [--compare FILE] [--repeat N]
#   scripts/aml/final.sh --summary DIR [--compare FILE]
#
# It extracts REV (default HEAD) with git archive into a temporary tree,
# never the working tree, builds mnemon there, and runs that tree's own
# scripts against that binary:
#   1. drill.sh: every scenario, --repeat N times (default 1), into DIR/drill;
#   2. load.sh --local --mode search, rates 0.25 to 3 a second, 300 s steps,
#      into DIR/load-search;
#   3. load.sh --local --mode add, rates 1 to 32 a second, 60 s steps settled
#      in between, into DIR/load-add.
# DIR is a new dated directory (default .testdata/aml/final-<UTC time>; it
# must not exist). It then prints, and writes to DIR/summary.md, the drill's
# matrix, the drill's numbers against an earlier run's scenario by scenario,
# and the knees with the capacity rows they give. FILE is the earlier run: a
# drill report.json (its matrix), or a matrix of rows with a run field
# (task16-matrix.json, whose "new" rows count); by default
# .testdata/aml/drill-task16/task16-matrix.json, skipped when absent.
#
# --summary DIR prints the summary of a run directory again, running nothing.
#
# It takes about two hours. Exit status: the drill's (0 when every hard
# criterion held, 1 when one did not), or 2 when a step could not run.
set -euo pipefail
umask 077
export LC_ALL=C

readonly PROG=aml-final
# shellcheck source=scripts/aml/lib.sh
. "$(dirname "$0")/lib.sh"
aml_trap_exit

REV=HEAD OUT= COMPARE=$AML_REPO/.testdata/aml/drill-task16/task16-matrix.json REPEAT=1 SUMMARY=
usage() {
	sed -n '5,6p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
}
while [ $# -gt 0 ]; do
	case $1 in --summary | --rev | --out | --compare | --repeat) ;; *) usage ;; esac
	# Every option takes a value: one without it is a usage error (2), never
	# an unbound $2 under set -u, whose 1 would read as a failed drill.
	[ $# -ge 2 ] || aml_die "$1 needs a value"
	case $1 in
	--summary) SUMMARY=$2 ;;
	--rev) REV=$2 ;;
	--out) OUT=$2 ;;
	--compare) COMPARE=$2 ;;
	--repeat) REPEAT=$2 ;;
	esac
	shift 2
done
# summary DIR: the drill's matrix, the drill against the earlier run
# ($COMPARE), and the knees, of the run in DIR.
summary() {
	local dir=$1 mode
	printf '# Final run of %s\n\n' "$(jq -r '.commit // "unknown"' "$dir/final.json" 2>/dev/null || echo unknown)"
	printf 'Drill: %s\n\n' "$(tail -n 1 "$dir/drill.out" 2>/dev/null || jq -r 'if .pass then "PASS" else "FAIL" end' "$dir/drill/report.json")"
	printf '## The drill matrix\n\n'
	cat "$dir/drill/matrix.md"
	printf '\n## The drill against the earlier run, scenario by scenario\n\n'
	if [ -s "$COMPARE" ]; then
		jq -r --slurpfile new "$dir/drill/report.json" '
			def norm: {name, run, searches, degraded, partial, p95: .searchP95,
				throttled: (.throttled // .attempts.throttled), invalid: (.invalid // .attempts.invalid), rejected: (.rejected // null),
				retries, trips: (.breakerTrips // .trips), drain: .drainSeconds, held: (.heldAfter // null), verdict};
			def cell($v): if $v == null then "-" elif ($v | type) == "number" then (if ($v | fabs) < 1e-9 then "0" else ($v * 100 | round / 100 | tostring) end) else ($v | tostring) end;
			def row($r): "\(cell($r.searches)) | \(cell($r.degraded)) | \(cell($r.partial)) | \(cell($r.p95)) | \(cell($r.throttled))/\(cell($r.invalid))/\(cell($r.rejected)) | \(cell($r.retries)) | \(cell($r.trips.deepseek))/\(cell($r.trips.jev)) | \(cell($r.drain)) | \(cell($r.held.deepseek))/\(cell($r.held.jev))";
			(if type == "array" then [.[] | select(.run == "new")] else .matrix end | map(norm)) as $old |
			"| scenario | run | earlier: Searches | degraded | partial | p95 s | throttled/invalid/rejected | retries | trips | drain s | held | new: Searches | degraded | partial | p95 s | throttled/invalid/rejected | retries | trips | drain s | held | new verdict |",
			"|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|",
			($new[0].matrix[] | norm as $n | ([$old[] | select(.name == $n.name)] | first) as $o |
				"| \($n.name) | \($n.run) | \(if $o == null then "- | - | - | - | - | - | - | - | -" else row($o) end) | \(row($n)) | \($n.verdict) |")' "$COMPARE"
	else
		printf 'No earlier run at %s.\n' "$COMPARE"
	fi
	printf '\n## The load knees (local, fake providers)\n\n'
	for mode in search add; do
		[ -s "$dir/load-$mode/report.json" ] || continue
		jq -r --arg mode "$mode" '
			"\($mode): " + (.knee | to_entries | map("\(.key) " + (if .value.knee == null then "no knee: \(.value.reason)"
				else "knee \(.value.knee.rate)/s, p50 \(.value.knee.p50 * 1000 | round / 1000) s, p95 \(.value.knee.p95 * 1000 | round / 1000) s, peak \(.value.knee.peakInFlight) in flight; baseline \(.value.baseline.rate)/s p50 \(.value.baseline.p50 * 1000 | round / 1000) s, p95 \(.value.baseline.p95 * 1000 | round / 1000) s\(if .value.void then " (\(.value.void))" else "" end)" end)) | join("; "))' \
			"$dir/load-$mode/report.json"
		jq -r '.steps[] | "  \(.route) \(.rate)/s: \(.requests) requests, codes \(.codes), p50 \(.p50 * 1000 | round / 1000) s, p95 \(.p95 * 1000 | round / 1000) s, p99 \(.p99 * 1000 | round / 1000) s, peak \(.peakInFlight) in flight\(if .settled == false then ", did not start settled" else "" end)"' "$dir/load-$mode/report.json"
	done
	printf '\nThe capacity rows (deploy/aml/README.md), local and with fake providers: the Search and Add knees above, their p50 and p95, and 60 times each knee as the requests a minute.\n'
}

if [ -n "$SUMMARY" ]; then
	[ -s "$SUMMARY/drill/report.json" ] || aml_die "$SUMMARY holds no drill/report.json"
	summary "$SUMMARY"
	exit 0
fi
aml_need git go tar jq
SHA=$(git -C "$AML_REPO" rev-parse --verify "$REV^{commit}" 2>/dev/null) || aml_die "$REV is no commit of $AML_REPO"
[ -n "$OUT" ] || OUT=$AML_REPO/.testdata/aml/final-$(date -u +%Y%m%dT%H%M%SZ)
[ ! -e "$OUT" ] || aml_die "$OUT exists: a final run writes a new directory"
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
TREE=$(mktemp -d "${TMPDIR:-/tmp}/aml-final.XXXXXX")
on_exit() {
	local status=$?
	rm -rf "$TREE"
	exit "$(aml_exit_status "$status")"
}
trap on_exit EXIT
trap 'exit 2' INT TERM

aml_say "extracting $SHA and building mnemon from it"
git -C "$AML_REPO" archive "$SHA" | tar -x -C "$TREE"
(cd "$TREE" && go build -o "$TREE/mnemon" .) || aml_die "the build of $SHA failed"
jq -n --arg sha "$SHA" --arg rev "$REV" --arg started "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg host "$(uname -sm)" \
	--argjson cpus "$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 0)" --arg go "$(cd "$TREE" && go env GOVERSION)" \
	'{kind: "aml-final-run", commit: $sha, rev: $rev, started: $started, host: $host, cpus: $cpus, go: $go,
	  built: "git archive of the commit, never the working tree"}' >"$OUT/final.json"

status=0
aml_say "the fault drill"
bash "$TREE/scripts/aml/drill.sh" --binary "$TREE/mnemon" --built-from "$SHA" --repeat "$REPEAT" --out "$OUT/drill" \
	>"$OUT/drill.out" 2>&1 || status=$?
[ "$status" -le 1 ] || aml_die "the drill could not run (exit $status; see $OUT/drill.out)"
aml_say "the load test: Searches"
bash "$TREE/scripts/aml/load.sh" --local --binary "$TREE/mnemon" --built-from "$SHA" --mode search \
	--rates 0.25,0.5,1,1.5,2,3 --out "$OUT/load-search" >"$OUT/load-search.out" 2>&1 ||
	aml_die "the Search load test could not run (see $OUT/load-search.out)"
aml_say "the load test: Adds"
bash "$TREE/scripts/aml/load.sh" --local --binary "$TREE/mnemon" --built-from "$SHA" --mode add \
	--rates 1,2,4,8,16,32 --out "$OUT/load-add" >"$OUT/load-add.out" 2>&1 ||
	aml_die "the Add load test could not run (see $OUT/load-add.out)"

summary "$OUT" | tee "$OUT/summary.md"
aml_say "final run: $OUT (summary.md, drill/, load-search/, load-add/)"
AML_EXIT_OURS=1
exit "$status"
