// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package expo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// buckets builds a Buckets value with the given offset and counts.
func buckets(offset int32, counts ...uint64) Buckets {
	bs := pmetric.NewExponentialHistogramDataPointBuckets()
	bs.SetOffset(offset)
	bs.BucketCounts().FromRaw(counts)
	return bs
}

func TestSpan(t *testing.T) {
	tests := []struct {
		name   string
		bs     Buckets
		expect int
	}{
		{name: "empty", bs: buckets(0), expect: 0},
		{name: "all-zero", bs: buckets(0, 0, 0, 0), expect: 0},
		{name: "single", bs: buckets(0, 0, 5, 0), expect: 1},
		{name: "dense", bs: buckets(0, 1, 2, 3), expect: 3},
		{name: "interior-gap", bs: buckets(0, 1, 0, 0, 4), expect: 4},
		{name: "leading-and-trailing-zeros", bs: buckets(0, 0, 0, 7, 0), expect: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, Span(tt.bs))
		})
	}
}

func TestCollapse(t *testing.T) {
	tests := []struct {
		name       string
		in         Buckets
		wantOffset int32
		wantCounts []uint64
	}{
		{
			// even offset: pairs are (0,1),(2,3),(4,5)
			name:       "even-offset",
			in:         buckets(0, 1, 2, 3, 4, 5, 6),
			wantOffset: 0,
			wantCounts: []uint64{3, 7, 11},
		},
		{
			// odd offset: the first bucket stands alone (its lower neighbor is at
			// an even index that does not belong to it), the offset drops to
			// floor(offset/2).
			name:       "odd-offset",
			in:         buckets(1, 1, 2, 3, 4),
			wantOffset: 0,
			wantCounts: []uint64{1, 5, 4},
		},
		{
			name:       "single-bucket-even",
			in:         buckets(2, 5),
			wantOffset: 1,
			wantCounts: []uint64{5},
		},
		{
			name:       "empty",
			in:         buckets(4),
			wantOffset: 2,
			wantCounts: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := sum(tt.in)
			Collapse(tt.in)
			Trim(tt.in)
			assert.Equal(t, tt.wantOffset, tt.in.Offset(), "offset")
			assert.Equal(t, tt.wantCounts, tt.in.BucketCounts().AsRaw(), "counts")
			assert.Equal(t, before, sum(tt.in), "total count must be conserved")
		})
	}
}

func TestDownscaleConservesCount(t *testing.T) {
	bs := buckets(0, 1, 2, 3, 4, 5, 6, 7, 8)
	before := sum(bs)
	Downscale(bs, 3, 0)
	assert.Equal(t, before, sum(bs))
}

func TestDownscaleRejectsUpscale(t *testing.T) {
	assert.Panics(t, func() {
		Downscale(buckets(0, 1, 2), 0, 3)
	})
}

func TestDownscaleNoOp(t *testing.T) {
	bs := buckets(0, 1, 2, 3)
	Downscale(bs, 2, 2)
	assert.Equal(t, []uint64{1, 2, 3}, bs.BucketCounts().AsRaw())
}

func TestTrim(t *testing.T) {
	tests := []struct {
		name       string
		in         Buckets
		wantOffset int32
		wantCounts []uint64
	}{
		{name: "no-zeros", in: buckets(3, 1, 2), wantOffset: 3, wantCounts: []uint64{1, 2}},
		{name: "leading", in: buckets(3, 0, 0, 5, 6), wantOffset: 5, wantCounts: []uint64{5, 6}},
		{name: "trailing", in: buckets(3, 5, 6, 0, 0), wantOffset: 3, wantCounts: []uint64{5, 6}},
		{name: "both", in: buckets(3, 0, 5, 6, 0), wantOffset: 4, wantCounts: []uint64{5, 6}},
		{name: "all-zero", in: buckets(3, 0, 0), wantOffset: 5, wantCounts: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Trim(tt.in)
			assert.Equal(t, tt.wantOffset, tt.in.Offset(), "offset")
			assert.Equal(t, tt.wantCounts, tt.in.BucketCounts().AsRaw(), "counts")
		})
	}
}

func TestLimit(t *testing.T) {
	t.Run("reduces-span-and-lowers-scale", func(t *testing.T) {
		dp := pmetric.NewExponentialHistogramDataPoint()
		dp.SetScale(4)
		dp.SetZeroCount(3)
		pos := dp.Positive()
		pos.SetOffset(0)
		pos.BucketCounts().FromRaw([]uint64{1, 1, 1, 1, 1, 1, 1, 1}) // span 8
		before := total(dp)

		Limit(dp, 3)

		assert.LessOrEqual(t, Span(dp.Positive()), 3, "positive span within limit")
		assert.Less(t, dp.Scale(), int32(4), "scale lowered")
		assert.Equal(t, before, total(dp), "total observation conserved")
	})

	t.Run("both-signs-share-scale", func(t *testing.T) {
		dp := pmetric.NewExponentialHistogramDataPoint()
		dp.SetScale(2)
		dp.Positive().BucketCounts().FromRaw([]uint64{1, 2, 3, 4, 5, 6}) // span 6
		dp.Negative().BucketCounts().FromRaw([]uint64{7, 8})             // span 2
		before := total(dp)

		Limit(dp, 3)

		assert.LessOrEqual(t, Span(dp.Positive()), 3)
		assert.LessOrEqual(t, Span(dp.Negative()), 3)
		assert.Equal(t, before, total(dp))
	})

	t.Run("already-within-limit-is-noop", func(t *testing.T) {
		dp := pmetric.NewExponentialHistogramDataPoint()
		dp.SetScale(2)
		dp.Positive().BucketCounts().FromRaw([]uint64{1, 2})

		Limit(dp, 5)

		assert.Equal(t, int32(2), dp.Scale())
		assert.Equal(t, []uint64{1, 2}, dp.Positive().BucketCounts().AsRaw())
	})

	t.Run("does-not-go-below-min-scale", func(t *testing.T) {
		dp := pmetric.NewExponentialHistogramDataPoint()
		dp.SetScale(MinScale)
		// impossible to satisfy at min scale; must terminate anyway.
		dp.Positive().BucketCounts().FromRaw([]uint64{1, 2, 3, 4, 5})

		Limit(dp, 2)

		assert.Equal(t, MinScale, dp.Scale())
	})

	t.Run("maxBuckets-below-one-is-noop", func(t *testing.T) {
		dp := pmetric.NewExponentialHistogramDataPoint()
		dp.SetScale(2)
		dp.Positive().BucketCounts().FromRaw([]uint64{1, 2, 3})
		Limit(dp, 0)
		assert.Equal(t, int32(2), dp.Scale())
	})
}

func TestScaleIdxBounds(t *testing.T) {
	// Round-trip: the index a value maps into must have bounds bracketing it.
	scale := Scale(2)
	for _, v := range []float64{0.5, 1.0, 1.5, 2.0, 3.7, 100.0} {
		idx := scale.Idx(v)
		lo, hi := scale.Bounds(idx)
		assert.LessOrEqual(t, lo, v, "lower bound of bucket %d for value %v", idx, v)
		assert.GreaterOrEqual(t, hi, v, "upper bound of bucket %d for value %v", idx, v)
	}
}

func sum(bs Buckets) uint64 {
	var s uint64
	counts := bs.BucketCounts()
	for i := 0; i < counts.Len(); i++ {
		s += counts.At(i)
	}
	return s
}

func total(dp DataPoint) uint64 {
	return sum(dp.Positive()) + sum(dp.Negative()) + dp.ZeroCount()
}
