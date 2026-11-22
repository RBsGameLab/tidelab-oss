// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"math"
	"math/rand/v2"
)

// Rng is the random numbers combat draws: Next(n) in [0, n), NextRange(lo, hi) in [lo, hi).
type Rng interface {
	Next(n int32) int32
	NextRange(lo, hi int32) int32
}

// SystemRng draws from math/rand (C#'s Random.Shared: not reproducible).
type SystemRng struct{}

func (SystemRng) Next(n int32) int32 {
	if n <= 0 {
		return 0
	}
	return rand.Int32N(n)
}

func (SystemRng) NextRange(lo, hi int32) int32 {
	if hi <= lo {
		return lo
	}
	return lo + rand.Int32N(hi-lo)
}

// DotNetRandom is .NET's new Random(seed): Knuth's subtractive generator as .NET implements it,
// so a seed gives the numbers the C# tests and robots saw.
type DotNetRandom struct {
	seeds         [56]int32
	inext, inextp int
}

const (
	mbig  = math.MaxInt32
	mseed = 161803398
)

func NewDotNetRandom(seed int32) *DotNetRandom {
	r := &DotNetRandom{}
	sub := int32(mbig)
	if seed != math.MinInt32 {
		sub = seed
		if sub < 0 {
			sub = -sub
		}
	}
	mj := int32(mseed) - sub
	r.seeds[55] = mj
	mk := int32(1)
	for i := 1; i < 55; i++ {
		ii := (21 * i) % 55
		r.seeds[ii] = mk
		mk = mj - mk
		if mk < 0 {
			mk += mbig
		}
		mj = r.seeds[ii]
	}
	for k := 1; k < 5; k++ {
		for i := 1; i < 56; i++ {
			n := i + 30
			if n >= 55 {
				n -= 55
			}
			r.seeds[i] -= r.seeds[1+n]
			if r.seeds[i] < 0 {
				r.seeds[i] += mbig
			}
		}
	}
	r.inext, r.inextp = 0, 21
	return r
}

func (r *DotNetRandom) internalSample() int32 {
	a, b := r.inext+1, r.inextp+1
	if a >= 56 {
		a = 1
	}
	if b >= 56 {
		b = 1
	}
	v := r.seeds[a] - r.seeds[b]
	if v == mbig {
		v--
	}
	if v < 0 {
		v += mbig
	}
	r.seeds[a] = v
	r.inext, r.inextp = a, b
	return v
}

// Sample is NextDouble: [0, 1).
func (r *DotNetRandom) Sample() float64 { return float64(r.internalSample()) * (1.0 / mbig) }

func (r *DotNetRandom) Next(n int32) int32 { return int32(r.Sample() * float64(n)) }

func (r *DotNetRandom) NextRange(lo, hi int32) int32 {
	return int32(r.Sample()*float64(int64(hi)-int64(lo))) + lo
}
