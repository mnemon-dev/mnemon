# scripts/aml/scenario.jq: the summary of one run of a drill scenario, with its
# criteria (drill.sh runs it: jq -n -f scripts/aml/scenario.jq, with the
# arguments it names). Its inputs: the scenario's row ($name, $class, $type,
# $spec, $what, $window, $effect, $want…); the times $tstart, $tfault, $tend
# and $tidle (null when the backlog did not drain); $residual, $heldSeconds,
# $without; the limits $minSearches, $drainLimit, $residualLimit and
# $recoverLimit; the requests' latencies and codes, and the second each
# Search was sent ($searchStarts); and, slurped, /admin/status ($sb, $sa),
# the metrics ($mb, $ma) and the fake provider's counts ($fb, $fa) before and
# after, the samples of /admin/status ($samples) and the degraded reasons
# ($reasons).
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
# The bound a degraded or partial column states, for a run that answered
# $answered Searches: null for - (not judged), {atMost} or {atLeast} for the
# others, with what it was derived from. Ruling R82's, fixed in advance:
#   - P^A, a fault random per attempt at rate P that the policy gives A
#     attempts: a Search makes callsPerSearch model calls, each failing when
#     all its attempts do (P^A), with a margin of 2 for chance: at most
#     max(1, ⌈N × min(1, 2·10·P^A)⌉) of the N Searches. The product is
#     rounded to 9 decimals before the ceiling, so that float noise
#     (0.05^2 × 20 × 20 = 1.0000000000000002) cannot add one.
#   - window+S, a fault over the window from $tfault to $tend: at most the
#     Searches that started in it or within S seconds after it.
def callsPerSearch: 10;
def round9: . * 1e9 | round / 1e9;
def bound($want; $answered):
	if $want == "-" then null
	elif $want == "0" then {form: $want, rule: "none may", atMost: 0}
	elif $want == ">0" then {form: $want, rule: "some must", atLeast: 1}
	elif ($want | test("^[0-9]+$")) then {form: $want, rule: "at most", atMost: ($want | tonumber)}
	elif ($want | startswith("window+")) then ($want | ltrimstr("window+") | tonumber) as $after
		| [$searchStarts[] | select(. >= $tfault and . <= $tend + $after)] as $started
		| {form: $want, rule: "a fault over the window", from: $tfault, to: ($tend + $after), atMost: ($started | length)}
	else ($want | split("^") | map(tonumber)) as [$p, $attempts]
		| ([1, 2 * callsPerSearch * pow($p; $attempts)] | min | round9) as $share
		| {form: $want, rule: "a fault random per attempt", rate: $p, attempts: $attempts, callsPerSearch: callsPerSearch,
			searches: $answered, share: $share, atMost: ([1, ($answered * $share | round9 | ceil)] | max)}
	end;
def within($bound; $value): if $bound == null then null elif $bound.atLeast != null then $value >= $bound.atLeast else $value <= $bound.atMost end;
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
		samples: ($samples | length),
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
	searchP95: $wantP95, breaker: $wantBreaker, breakersClosedWithin: $recoverLimit, retries: $wantRetries, drainSeconds: $drainLimit,
	residualBelow: $residualLimit}
| .bounds = {degraded: bound($wantDegraded; .searches.answered), partial: bound($wantPartial; .searches.answered)}
| .criteria = ({
	searches: (.searches.answered >= $minSearches),
	effect: (if $effect == "none" then .effect.value == 0 else .effect.value > 0 end),
	non2xx: (.requests.search.non2xx + .requests.add.non2xx == 0),
	held: (.held.seconds != null),
	withoutJEV: (.withoutJEVLines == 0 and .linkFallbacks == 0),
	degraded: within(.bounds.degraded; .searches.degraded),
	partial: within(.bounds.partial; .searches.partial),
	searchP95: (if $wantP95 == "-" then null else (.latency.search.p95 // 0) <= ($wantP95 | tonumber) end),
	# cycle: a breaker opened during the fault, and every one was closed
	# again within $recoverLimit seconds of its end. steady: none opened,
	# and there are samples to show it.
	breaker: (if $wantBreaker == "cycle" then .recovery.openedDuringFault and .recovery.breakersClosedSeconds != null
			and .recovery.breakersClosedSeconds <= $recoverLimit
		elif $wantBreaker == "steady" then .recovery.samples > 0 and (.breakerTrips.deepseek + .breakerTrips.jev == 0) and (.recovery.opened | not)
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
