// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package classic

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// histogram builds a classic histogram data point from bounds and counts.
// len(counts) must be len(bounds)+1.
func histogram(bounds []float64, counts []uint64) DataPoint {
	dp := pmetric.NewHistogramDataPoint()
	dp.ExplicitBounds().FromRaw(bounds)
	dp.BucketCounts().FromRaw(counts)
	var total uint64
	for _, c := range counts {
		total += c
	}
	dp.SetCount(total)
	return dp
}

func sum(counts pcommon.UInt64Slice) uint64 {
	var s uint64
	for i := 0; i < counts.Len(); i++ {
		s += counts.At(i)
	}
	return s
}

// TestMergeCount exercises the positional "count" strategy and the bias lever
// that decides where the leftover buckets land.
func TestMergeCount(t *testing.T) {
	tests := []struct {
		name       string
		bounds     []float64
		counts     []uint64
		maxBuckets int
		bias       Bias
		wantBounds []float64
		wantCounts []uint64
	}{
		{
			// 7 buckets → 3 groups. base=2, rem=1; bias upper puts the larger
			// group at the bottom: sizes [3,2,2], keeping bounds at index 2 and 4.
			name:       "seven-to-three-upper",
			bounds:     []float64{1, 2, 3, 4, 5, 6},
			counts:     []uint64{1, 2, 3, 4, 5, 6, 7},
			maxBuckets: 3,
			bias:       BiasUpper,
			wantBounds: []float64{3, 5},
			wantCounts: []uint64{6, 9, 13},
		},
		{
			// 10 buckets → 4 groups. base=2, rem=2; bias upper => sizes [3,3,2,2],
			// finer resolution at the top. This is the user's "3,3,2,2" split.
			name:       "ten-to-four-upper",
			bounds:     []float64{1, 2, 3, 4, 5, 6, 7, 8, 9},
			counts:     []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			maxBuckets: 4,
			bias:       BiasUpper,
			wantBounds: []float64{3, 6, 8},
			wantCounts: []uint64{6, 15, 15, 19},
		},
		{
			// Same input, bias lower => sizes [2,2,3,3], finer at the bottom.
			// This is the user's "2,2,3,3" split.
			name:       "ten-to-four-lower",
			bounds:     []float64{1, 2, 3, 4, 5, 6, 7, 8, 9},
			counts:     []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			maxBuckets: 4,
			bias:       BiasLower,
			wantBounds: []float64{2, 4, 7},
			wantCounts: []uint64{3, 7, 18, 27},
		},
		{
			name:       "preserves-inf-overflow",
			bounds:     []float64{1, 2, 3, 4},
			counts:     []uint64{0, 0, 0, 0, 99}, // all mass in +Inf bucket
			maxBuckets: 2,
			bias:       BiasUpper,
			wantBounds: []float64{3},
			wantCounts: []uint64{0, 99},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp := histogram(tt.bounds, tt.counts)
			before := sum(dp.BucketCounts())

			Merge(dp, Options{MaxBuckets: tt.maxBuckets, Strategy: StrategyCount, Bias: tt.bias})

			assert.Equal(t, tt.wantBounds, dp.ExplicitBounds().AsRaw(), "bounds")
			assert.Equal(t, tt.wantCounts, dp.BucketCounts().AsRaw(), "counts")
			assert.Equal(t, before, sum(dp.BucketCounts()), "total count conserved")
			assert.Equal(t, dp.BucketCounts().Len(), dp.ExplicitBounds().Len()+1, "well-formed")
			assert.LessOrEqual(t, dp.BucketCounts().Len(), tt.maxBuckets, "within limit")
		})
	}
}

// TestMergeLinear keeps the bounds nearest to evenly spaced (linear) targets.
func TestMergeLinear(t *testing.T) {
	tests := []struct {
		name       string
		bounds     []float64
		counts     []uint64
		maxBuckets int
		wantBounds []float64
	}{
		{
			// Evenly spaced input: targets 10,50,90 land exactly on bounds.
			name:       "even-input",
			bounds:     []float64{10, 20, 30, 40, 50, 60, 70, 80, 90},
			counts:     []uint64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
			maxBuckets: 4,
			wantBounds: []float64{10, 50, 90},
		},
		{
			// Geometric input, linear spacing: targets 1 and 128 keep the extremes.
			name:       "geometric-input-endpoints",
			bounds:     []float64{1, 2, 4, 8, 16, 32, 64, 128},
			counts:     []uint64{1, 1, 1, 1, 1, 1, 1, 1, 1},
			maxBuckets: 3,
			wantBounds: []float64{1, 128},
		},
		{
			// Geometric input, three kept bounds: mid target 64.5 -> nearest 64.
			name:       "geometric-input-mid",
			bounds:     []float64{1, 2, 4, 8, 16, 32, 64, 128},
			counts:     []uint64{1, 1, 1, 1, 1, 1, 1, 1, 1},
			maxBuckets: 4,
			wantBounds: []float64{1, 64, 128},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp := histogram(tt.bounds, tt.counts)
			before := sum(dp.BucketCounts())

			Merge(dp, Options{MaxBuckets: tt.maxBuckets, Strategy: StrategyLinear, Bias: BiasUpper})

			assert.Equal(t, tt.wantBounds, dp.ExplicitBounds().AsRaw(), "bounds")
			assert.Equal(t, before, sum(dp.BucketCounts()), "total count conserved")
			assert.LessOrEqual(t, dp.BucketCounts().Len(), tt.maxBuckets, "within limit")
		})
	}
}

// TestMergeLogarithmic keeps the bounds nearest to evenly spaced log targets.
func TestMergeLogarithmic(t *testing.T) {
	// Powers of ten, maxBuckets 4 -> keep 3. The middle log target lands exactly
	// on 100, so log keeps {1,100,10000}. Linear's middle target (5000.5) lands
	// nearest 1000, so it keeps {1,1000,10000} -- a clean differentiator.
	bounds := []float64{1, 10, 100, 1000, 10000}
	counts := []uint64{1, 2, 3, 4, 5, 6}

	dp := histogram(bounds, counts)
	before := sum(dp.BucketCounts())
	Merge(dp, Options{MaxBuckets: 4, Strategy: StrategyLogarithmic, Bias: BiasUpper})
	assert.Equal(t, []float64{1, 100, 10000}, dp.ExplicitBounds().AsRaw(), "log bounds")
	assert.Equal(t, []uint64{1, 5, 9, 6}, dp.BucketCounts().AsRaw(), "log counts")
	assert.Equal(t, before, sum(dp.BucketCounts()), "total count conserved")

	lin := histogram(bounds, counts)
	Merge(lin, Options{MaxBuckets: 4, Strategy: StrategyLinear, Bias: BiasUpper})
	assert.Equal(t, []float64{1, 1000, 10000}, lin.ExplicitBounds().AsRaw(), "linear differs")
}

// TestMergeLogarithmicFallsBackToLinear: log spacing is undefined for
// non-positive bounds, so it must degrade to linear rather than misbehave.
func TestMergeLogarithmicFallsBackToLinear(t *testing.T) {
	bounds := []float64{0, 10, 20, 30, 40, 50, 60, 70, 80, 90}
	counts := []uint64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}

	log := histogram(bounds, counts)
	Merge(log, Options{MaxBuckets: 4, Strategy: StrategyLogarithmic, Bias: BiasUpper})

	lin := histogram(bounds, counts)
	Merge(lin, Options{MaxBuckets: 4, Strategy: StrategyLinear, Bias: BiasUpper})

	assert.Equal(t, lin.ExplicitBounds().AsRaw(), log.ExplicitBounds().AsRaw(),
		"logarithmic must fall back to linear when a bound is <= 0")
}

// TestMergeRetainBounds pins SLA boundaries so they always survive the merge.
func TestMergeRetainBounds(t *testing.T) {
	tests := []struct {
		name       string
		retain     []float64
		maxBuckets int
		wantBounds []float64
	}{
		{
			// Baseline count merge is {3,6,8}. Pinning 5 forces it in and drops the
			// nearest auto-selected bound (6), giving {3,5,8}.
			name:       "pin-displaces-nearest",
			retain:     []float64{5},
			maxBuckets: 4,
			wantBounds: []float64{3, 5, 8},
		},
		{
			// A pin that is not an existing bound cannot be created; ignored.
			name:       "pin-absent-is-ignored",
			retain:     []float64{100},
			maxBuckets: 4,
			wantBounds: []float64{3, 6, 8},
		},
		{
			// More pins than the budget allows: pins win, max_buckets is exceeded.
			name:       "pins-exceed-budget-win",
			retain:     []float64{3, 6, 8},
			maxBuckets: 3,
			wantBounds: []float64{3, 6, 8},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp := histogram(
				[]float64{1, 2, 3, 4, 5, 6, 7, 8, 9},
				[]uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			)
			before := sum(dp.BucketCounts())

			Merge(dp, Options{
				MaxBuckets: tt.maxBuckets,
				Strategy:   StrategyCount,
				Bias:       BiasUpper,
				Retain:     tt.retain,
			})

			assert.Equal(t, tt.wantBounds, dp.ExplicitBounds().AsRaw(), "bounds")
			assert.Equal(t, before, sum(dp.BucketCounts()), "total count conserved")
			assert.Equal(t, dp.BucketCounts().Len(), dp.ExplicitBounds().Len()+1, "well-formed")
		})
	}
}

// TestMergeDefaults: an empty strategy/bias must behave as logarithmic/upper.
func TestMergeDefaults(t *testing.T) {
	bounds := []float64{1, 2, 4, 8, 16, 32, 64, 128}
	counts := []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9}

	got := histogram(bounds, counts)
	Merge(got, Options{MaxBuckets: 4})

	want := histogram(bounds, counts)
	Merge(want, Options{MaxBuckets: 4, Strategy: StrategyLogarithmic, Bias: BiasUpper})

	assert.Equal(t, want.ExplicitBounds().AsRaw(), got.ExplicitBounds().AsRaw())
	assert.Equal(t, want.BucketCounts().AsRaw(), got.BucketCounts().AsRaw())
}

func TestMergeNoOp(t *testing.T) {
	t.Run("already-within-limit", func(t *testing.T) {
		dp := histogram([]float64{1, 2}, []uint64{5, 6, 7})
		Merge(dp, Options{MaxBuckets: 5, Strategy: StrategyCount})
		assert.Equal(t, []float64{1, 2}, dp.ExplicitBounds().AsRaw())
		assert.Equal(t, []uint64{5, 6, 7}, dp.BucketCounts().AsRaw())
	})

	t.Run("exactly-at-limit", func(t *testing.T) {
		dp := histogram([]float64{1, 2}, []uint64{5, 6, 7})
		Merge(dp, Options{MaxBuckets: 3, Strategy: StrategyCount})
		assert.Equal(t, []float64{1, 2}, dp.ExplicitBounds().AsRaw())
		assert.Equal(t, []uint64{5, 6, 7}, dp.BucketCounts().AsRaw())
	})

	t.Run("maxBuckets-below-two", func(t *testing.T) {
		dp := histogram([]float64{1, 2}, []uint64{5, 6, 7})
		Merge(dp, Options{MaxBuckets: 1, Strategy: StrategyCount})
		assert.Equal(t, []uint64{5, 6, 7}, dp.BucketCounts().AsRaw())
	})

	t.Run("malformed-bounds", func(t *testing.T) {
		dp := pmetric.NewHistogramDataPoint()
		dp.ExplicitBounds().FromRaw([]float64{1, 2, 3, 4, 5})
		dp.BucketCounts().FromRaw([]uint64{1, 2, 3})
		Merge(dp, Options{MaxBuckets: 2, Strategy: StrategyCount})
		assert.Equal(t, []uint64{1, 2, 3}, dp.BucketCounts().AsRaw())
	})
}

// TestMergeProducesExactBucketCount: without pins every stable strategy must
// land on exactly maxBuckets buckets and stay well-formed and count-conserving.
func TestMergeProducesExactBucketCount(t *testing.T) {
	for _, strat := range []Strategy{StrategyCount, StrategyLinear, StrategyLogarithmic} {
		for n := 5; n <= 40; n++ {
			bounds := make([]float64, n-1)
			counts := make([]uint64, n)
			for i := range bounds {
				bounds[i] = math.Pow(2, float64(i)) // positive & monotonic for log
			}
			for i := range counts {
				counts[i] = uint64(i + 1)
			}
			dp := histogram(bounds, counts)
			before := sum(dp.BucketCounts())

			Merge(dp, Options{MaxBuckets: 4, Strategy: strat, Bias: BiasUpper})

			assert.Equal(t, 4, dp.BucketCounts().Len(), "%s n=%d", strat, n)
			assert.Equal(t, 3, dp.ExplicitBounds().Len(), "%s n=%d", strat, n)
			assert.Equal(t, before, sum(dp.BucketCounts()), "%s n=%d count conserved", strat, n)
		}
	}
}
