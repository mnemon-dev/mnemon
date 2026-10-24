# scripts/aml/knee.jq: the knee rule of load.sh, a jq module
# (jq -L scripts/aml 'include "knee"; …'). A step is one route's numbers at
# one rate: {rate, requests, non2xx, p50, p95, settled, …}, the steps of a
# route in rising rates, the first of them the baseline; settled says whether
# the step started from a consolidation scheduler with no work (true, false,
# or null when it was not checked).

# missed($base): whether this step misses the knee criterion against the
# baseline step: its p95 above twice the baseline's, its p50 above twice the
# baseline's, or a non-2xx answer.
def missed($base): .p95 > 2 * $base.p95 or .p50 > 2 * $base.p50 or .non2xx > 0;

# knee($route; $validate; $minBaseline): the knee of a route's steps (the
# input, an array): the highest rate whose step, and every step below it,
# does not miss the criterion; {knee, baseline}, with void naming the rate
# from which Add steps were void, or {knee: null, reason}. For Adds, a step
# that did not start settled inherited a backlog: it is void, and so is
# every step after it, and the knee comes from the steps before it. There is
# none in a validation run, when the baseline answered fewer than
# $minBaseline requests, or, for Adds, when the baseline did not start
# settled.
def knee($route; $validate; $minBaseline):
	. as $all
	| (if $route == "add" then (first(range($all | length) as $i | select($all[$i].settled != true) | $i) // ($all | length)) else ($all | length) end) as $cut
	| $all[:$cut] as $s | $s[0] as $base
	| if $validate then {knee: null, reason: "a validation run (--validate): its steps are shorter than the method asks"}
	elif ($s | length) == 0 then
		{knee: null, reason: "the baseline step did not start from a settled scheduler (admin access and --settle are needed)"}
	elif ($base.requests - $base.non2xx) < $minBaseline then
		{knee: null, reason: "the baseline step answered \($base.requests - $base.non2xx) requests, fewer than \($minBaseline)"}
	else (reduce $s[] as $x ({ok: true, knee: null}; if .ok and ($x | missed($base) | not) then .knee = $x else .ok = false end) | .knee) as $k
		| if $k == null then {knee: null, reason: "the baseline step itself had non-2xx answers"}
		  else {knee: ($k | {rate, p50, p95, p99, peakInFlight, requests}), baseline: ($base | {rate, p50, p95, requests})} end
		| if $cut < ($all | length) then
			.void = "the step at \($all[$cut].rate)/s did not start from a settled scheduler: the steps from \($all[$cut].rate)/s on are void"
				+ (if $k != null and $k.rate == $s[-1].rate then ", and the knee may be higher" else "" end)
		  else . end
	end;
