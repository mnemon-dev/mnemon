package records

import "math"

// jsLog preserves the log operation used by the existing Node/V8 reference.
// Go's math.Log can differ by one ULP, enough to change an exact BM25 tie.
// Port of the existing reference runtime's fdlibm log operation.
// Source: node v25.1.0 deps/v8/src/base/ieee754.cc, log(double).
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
// Developed at SunSoft, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this software is freely
// granted, provided that this notice is preserved.
// Modified significantly by Google Inc. Copyright 2016 the V8 project authors.
// All rights reserved. See LICENSE.v8 for the V8 license.
// Explicit FMA operations preserve the contractions in the reference V8
// arm64 build. BM25 operators outside Math.log round separately.
func jsLog(x float64) float64 {
	const (
		ln2Hi = 6.93147180369123816490e-01
		ln2Lo = 1.90821492927058770002e-10
		lg1   = 6.666666666666735130e-01
		lg2   = 3.999999999940941908e-01
		lg3   = 2.857142874366239149e-01
		lg4   = 2.222219843214978396e-01
		lg5   = 1.818357216161805012e-01
		lg6   = 1.531383769920937332e-01
		lg7   = 1.479819860511658591e-01
	)
	if x == 0 {
		return math.Inf(-1)
	}
	if x < 0 {
		return math.NaN()
	}
	if math.IsNaN(x) || math.IsInf(x, 1) {
		return x
	}
	bits := math.Float64bits(x)
	hx := int32(bits >> 32)
	k := int32(0)
	if hx < 0x00100000 {
		k -= 54
		x *= 1.80143985094819840000e+16
		bits = math.Float64bits(x)
		hx = int32(bits >> 32)
	}
	k += (hx >> 20) - 1023
	hx &= 0x000fffff
	i := (hx + 0x95f64) & 0x100000
	x = math.Float64frombits((uint64(uint32(hx|(i^0x3ff00000))) << 32) | (bits & 0xffffffff))
	k += i >> 20
	f := x - 1
	dk := float64(k)

	if (0x000fffff & (2 + hx)) < 3 {
		if f == 0 {
			if k == 0 {
				return 0
			}
			return math.FMA(dk, ln2Hi, dk*ln2Lo)
		}
		r := float64(f * f * math.FMA(-0.33333333333333333, f, 0.5))
		if k == 0 {
			return f - r
		}
		return math.FMA(dk, ln2Hi, -(math.FMA(-dk, ln2Lo, r) - f))
	}
	s := f / (2 + f)
	z := s * s
	i = hx - 0x6147a
	w := z * z
	j := 0x6b851 - hx
	t1 := math.FMA(w, math.FMA(w, lg6, lg4), lg2)
	t2 := math.FMA(w, math.FMA(w, math.FMA(w, lg7, lg5), lg3), lg1)
	r := float64(w*t1) + float64(z*t2)
	i |= j
	if i > 0 {
		hfsq := float64(0.5 * f * f)
		if k == 0 {
			return f + math.FMA(s, hfsq+r, -hfsq)
		}
		p := math.FMA(s, hfsq+r, dk*ln2Lo)
		return math.FMA(dk, ln2Hi, -((hfsq - p) - f))
	}
	if k == 0 {
		return math.FMA(-s, f-r, f)
	}
	p := math.FMA(s, f-r, -dk*ln2Lo)
	return math.FMA(dk, ln2Hi, -(p - f))
}
