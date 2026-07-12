// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package expo implements bucket-count reducing operations on exponential
// histograms.
//
// The Collapse / Downscale algorithm is adapted from the exponential-histogram
// helpers in processor/deltatocumulativeprocessor/internal/data/expo, which are
// not importable across module boundaries.
package expo // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/bucketmergeprocessor/internal/expo"

import (
	"fmt"
	"math"

	"go.opentelemetry.io/collector/pdata/pmetric"
)

type (
	// DataPoint is an exponential histogram data point.
	DataPoint = pmetric.ExponentialHistogramDataPoint
	// Buckets are the positive or negative buckets of a data point.
	Buckets = pmetric.ExponentialHistogramDataPointBuckets
)

// MinScale is the smallest scale permitted by the data model. Merging buckets
// lowers the scale; it never goes below this bound.
//
// https://opentelemetry.io/docs/specs/otel/metrics/data-model/#exponential-scale
const MinScale int32 = -10

// Scale is the resolution of an exponential histogram.
type Scale int32

// Idx gives the bucket index v belongs into.
func (scale Scale) Idx(v float64) int {
	// from: https://opentelemetry.io/docs/specs/otel/metrics/data-model/#all-scales-use-the-logarithm-function
	if frac, exp := math.Frexp(v); frac == 0.5 {
		return ((exp - 1) << scale) - 1
	}
	scaleFactor := math.Ldexp(math.Log2E, int(scale))
	return int(math.Floor(math.Log(v) * scaleFactor))
}

// Bounds returns the half-open interval (min,max] covered by the bucket at index.
func (scale Scale) Bounds(index int) (minVal, maxVal float64) {
	lower := func(index int) float64 {
		inverseFactor := math.Ldexp(math.Ln2, int(-scale))
		return math.Exp(float64(index) * inverseFactor)
	}
	return lower(index), lower(index + 1)
}

// Span returns the number of buckets between the first and last populated
// (non-zero) bucket, inclusive. It is zero when no bucket is populated.
func Span(bs Buckets) int {
	counts := bs.BucketCounts()
	lo, hi := -1, -1
	for i := 0; i < counts.Len(); i++ {
		if counts.At(i) != 0 {
			if lo == -1 {
				lo = i
			}
			hi = i
		}
	}
	if lo == -1 {
		return 0
	}
	return hi - lo + 1
}

// Limit lowers the scale of dp, merging adjacent buckets, until neither the
// positive nor the negative populated span exceeds maxBuckets, or MinScale is
// reached. It is a no-op when the data point already fits or maxBuckets < 1.
//
// The observation is preserved exactly (total counts are conserved); only the
// resolution is reduced. Both signs share a single scale, so they are collapsed
// together.
func Limit(dp DataPoint, maxBuckets int) {
	if maxBuckets < 1 {
		return
	}
	pos, neg := dp.Positive(), dp.Negative()
	for dp.Scale() > MinScale && (Span(pos) > maxBuckets || Span(neg) > maxBuckets) {
		Collapse(pos)
		Collapse(neg)
		dp.SetScale(dp.Scale() - 1)
	}
	Trim(pos)
	Trim(neg)
}

// Downscale collapses the buckets of bs until scale 'to' is reached.
func Downscale(bs Buckets, from, to Scale) {
	switch {
	case from == to:
		return
	case from < to:
		// even distribution within a bucket cannot be assumed, so upscaling
		// (splitting) would fabricate data.
		panic(fmt.Sprintf("cannot upscale without introducing error (%d -> %d)", from, to))
	}
	for at := from; at > to; at-- {
		Collapse(bs)
	}
}

// Collapse merges adjacent buckets, halving the resolution (scale-1). Due to the
// "perfect subsetting" property of exponential histograms this preserves the
// observation. The counts slice keeps its length; the now-unused upper half is
// zeroed and left in place (see Trim to compact it).
func Collapse(bs Buckets) {
	counts := bs.BucketCounts()

	offsetWasOdd := bs.Offset()%2 != 0
	shift := 0
	if offsetWasOdd {
		shift--
	}

	size := counts.Len() / 2
	if counts.Len()%2 != 0 || offsetWasOdd {
		size++
	}

	if offsetWasOdd {
		bs.SetOffset(bs.Offset() - 1)
	}
	bs.SetOffset(bs.Offset() / 2)

	if counts.Len() == 0 {
		return
	}

	for i := 0; i < size; i++ {
		k := i*2 + shift
		if i == 0 && k == -1 {
			counts.SetAt(i, counts.At(k+1))
			continue
		}
		counts.SetAt(i, counts.At(k))
		if k+1 < counts.Len() {
			counts.SetAt(i, counts.At(k)+counts.At(k+1))
		}
	}

	for i := size; i < counts.Len(); i++ {
		counts.SetAt(i, 0)
	}
}

// Trim removes leading and trailing zero buckets, adjusting the offset so the
// representation is compact. An all-zero (or empty) bucket set becomes empty.
func Trim(bs Buckets) {
	counts := bs.BucketCounts()
	n := counts.Len()
	lo, hi := 0, n
	for lo < n && counts.At(lo) == 0 {
		lo++
	}
	for hi > lo && counts.At(hi-1) == 0 {
		hi--
	}
	if lo == 0 && hi == n {
		return
	}
	bs.SetOffset(bs.Offset() + int32(lo))
	if lo >= hi {
		counts.FromRaw(nil)
		return
	}
	counts.FromRaw(counts.AsRaw()[lo:hi])
}
