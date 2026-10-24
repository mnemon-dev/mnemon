# scripts/aml/knee.jq: the knee rule of load.sh, a jq module
# (jq -L scripts/aml 'include "knee"; …'). A step is one route's numbers at
# one rate: {rate, requests, non2xx, p50, p95, …}, the steps of a route in
# rising rates, the first of them the baseline.

# missed($base): whether this step misses the knee criterion against the
# baseline step: its p95 above twice the baseline's, its p50 above twice the
# baseline's, or a non-2xx answer.
def missed($base): .p95 > 2 * $base.p95 or .p50 > 2 * $base.p50 or .non2xx > 0;

# knee($route; $validate; $minBaseline; $settled): the knee of a route's
# steps (the input, an array): the highest rate whose step, and every step
# below it, does not miss the criterion; {knee, baseline}, or {knee: null,
# reason}. There is none in a validation run, when the baseline answered
# fewer than $minBaseline requests, or, for Adds, when not every step started
# from a settled consolidation scheduler.
def knee($route; $validate; $minBaseline; $settled):
	. as $s | $s[0] as $base |
	if $validate then {knee: null, reason: "a validation run (--validate): its steps are shorter than the method asks"}
	elif ($base.requests - $base.non2xx) < $minBaseline then
		{knee: null, reason: "the baseline step answered \($base.requests - $base.non2xx) requests, fewer than \($minBaseline)"}
	elif $route == "add" and ($settled | not) then
		{knee: null, reason: "the steps did not all start from a settled scheduler (admin access and --settle are needed)"}
	else (reduce $s[] as $x ({ok: true, knee: null}; if .ok and ($x | missed($base) | not) then .knee = $x else .ok = false end) | .knee) as $k
		| if $k == null then {knee: null, reason: "the baseline step itself had non-2xx answers"}
		  else {knee: ($k | {rate, p50, p95, p99, peakInFlight, requests}), baseline: ($base | {rate, p50, p95, requests})} end
	end;
