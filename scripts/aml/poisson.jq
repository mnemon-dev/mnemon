# scripts/aml/poisson.jq: the Poisson quantile that bounds the Searches a random
# fault degrades (ruling R84). A jq module: scenario.jq includes it with
# `include "poisson";` and jq's -L scripts/aml.

# poissonQuantile($mean; $tail): the smallest integer k >= 0 with P(X > k) <
# $tail, for X Poisson with the mean $mean. It sums the probabilities from 0 up
# by their recurrence, p(k) = p(k-1) * mean / k, taken in logarithms because
# e^-mean is 0 in a double from a mean of about 745 on, until the upper tail
# falls below $tail. A mean of 0 or less has no event (0). A mean that is not a
# finite number, or a tail outside (0, 1), is an error, and so is a sum that
# has not got there by mean + 40 standard deviations + 100: it must never loop
# for ever. The sum is in doubles: it agrees with exact arithmetic for tails of
# 1e-10 and above (checked for means from 0.001 to 1000), and for a far smaller
# tail it can return a k a little off, or fail as above. The drill passes 1%.
def poissonQuantile($mean; $tail):
	if ($mean | type) != "number" or ($mean | isnan) or ($mean | isinfinite) then error("poissonQuantile: the mean must be a finite number")
	elif ($tail | type) != "number" or $tail <= 0 or $tail >= 1 then error("poissonQuantile: the tail must be in (0, 1)")
	elif $mean <= 0 then 0
	else
		($mean | log) as $logMean
		| ($mean + 40 * ($mean | sqrt) + 100 | ceil) as $last
		| {k: 0, logP: (0 - $mean), cdf: (0 - $mean | exp)}
		| until(1 - .cdf < $tail or .k >= $last; .k += 1 | .logP += ($logMean - (.k | log)) | .cdf += (.logP | exp))
		| if 1 - .cdf < $tail then .k else error("poissonQuantile: the upper tail is still \(1 - .cdf) at k = \(.k)") end
	end;
