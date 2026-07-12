// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package classic implements bucket-count reducing operations on classic
// (explicit-bounds) histograms.
//
// Merging a classic histogram can only ever drop interior boundaries and sum
// the counts on either side; it can never invent a new boundary. Every strategy
// is therefore a rule for choosing which of the existing bounds to keep. The
// strategies that look only at the bound values ("count", "linear",
// "logarithmic") are deterministic given the input bounds, so every data point
// sharing a bound layout is reduced to the same layout — the "le" set stays
// stable across data points and series, which downstream aggregation relies on.
package classic // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/bucketmergeprocessor/internal/classic"

import (
	"math"
	"sort"

	"go.opentelemetry.io/collector/pdata/pmetric"
)

// DataPoint is a classic (explicit-bounds) histogram data point.
type DataPoint = pmetric.HistogramDataPoint

// Strategy selects how the surviving bounds are chosen when a histogram has
// more buckets than the configured limit.
type Strategy string

const (
	// StrategyCount groups buckets so that each surviving bucket folds in a
	// comparable number of original buckets. It ignores the bound values.
	StrategyCount Strategy = "count"
	// StrategyLinear keeps the bounds nearest to evenly spaced targets on a
	// linear axis, so surviving buckets have comparable widths.
	StrategyLinear Strategy = "linear"
	// StrategyLogarithmic keeps the bounds nearest to evenly spaced targets on a
	// log axis, so surviving buckets have comparable ratios. It falls back to
	// StrategyLinear when any bound is <= 0 (log is undefined there).
	StrategyLogarithmic Strategy = "logarithmic"

	// DefaultStrategy is applied when Options.Strategy is empty.
	DefaultStrategy = StrategyLogarithmic
)

// Bias decides which end keeps finer resolution when the buckets do not divide
// evenly and one end must absorb the remainder.
type Bias string

const (
	// BiasLower keeps finer resolution at the low end (larger groups at the top).
	BiasLower Bias = "lower"
	// BiasUpper keeps finer resolution at the high end (larger groups at the
	// bottom). This is the default and suits latency/size histograms.
	BiasUpper Bias = "upper"

	// DefaultBias is applied when Options.Bias is empty.
	DefaultBias = BiasUpper
)

// Options controls a single Merge call.
type Options struct {
	// MaxBuckets is the maximum number of bucket counts to retain, including the
	// implicit +Inf overflow bucket.
	MaxBuckets int
	// Strategy selects how surviving bounds are chosen. Empty means DefaultStrategy.
	Strategy Strategy
	// Bias breaks ties when the buckets do not divide evenly. Empty means DefaultBias.
	Bias Bias
	// Retain lists bound values that must survive the merge if present in the
	// input. They take priority over the strategy; if they alone exceed the
	// budget, MaxBuckets is treated as best-effort and all of them are kept.
	Retain []float64
}

// Merge reduces dp to at most opts.MaxBuckets bucket counts by dropping interior
// boundaries and summing the counts between the survivors.
//
// Count, Sum, Min and Max are aggregate quantities and are left untouched. Data
// points that already fit, or that are not well-formed, are left unchanged.
func Merge(dp DataPoint, opts Options) {
	counts := dp.BucketCounts()
	bounds := dp.ExplicitBounds()

	n := counts.Len()
	// A well-formed classic histogram has exactly one more bucket count than
	// explicit bound. Refuse to touch anything that violates that invariant, or
	// that already fits.
	if opts.MaxBuckets < 2 || n <= opts.MaxBuckets || bounds.Len()+1 != n {
		return
	}

	strategy := opts.Strategy
	if strategy == "" {
		strategy = DefaultStrategy
	}
	bias := opts.Bias
	if bias == "" {
		bias = DefaultBias
	}

	rawBounds := bounds.AsRaw()
	keep := selectBounds(rawBounds, opts.MaxBuckets-1, strategy, bias, opts.Retain)

	newBounds := make([]float64, len(keep))
	for i, idx := range keep {
		newBounds[i] = rawBounds[idx]
	}

	// Sum each original bucket into the group bounded above by the next surviving
	// bound: group(i) = number of kept bound indices strictly less than i.
	newCounts := make([]uint64, len(keep)+1)
	for i := 0; i < n; i++ {
		g := sort.SearchInts(keep, i)
		newCounts[g] += counts.At(i)
	}

	counts.FromRaw(newCounts)
	bounds.FromRaw(newBounds)
}

// selectBounds returns the sorted indices of the bounds to keep (at most keep of
// them, more only when Retain pins force it), per the chosen strategy.
func selectBounds(bounds []float64, keep int, strategy Strategy, bias Bias, retain []float64) []int {
	var selected []int
	switch strategy {
	case StrategyLinear:
		selected = selectByValue(bounds, keep, bias, false)
	case StrategyLogarithmic:
		selected = selectByValue(bounds, keep, bias, true)
	default: // StrategyCount
		selected = selectByCount(len(bounds), keep, bias)
	}
	return applyRetain(bounds, selected, keep, retain)
}

// selectByCount partitions k+1 buckets into keep+1 contiguous groups of as-equal
// size as possible and returns the interior bound indices between groups. The
// remainder buckets go to the bottom (BiasUpper, finer at the top) or the top
// (BiasLower, finer at the bottom).
func selectByCount(k, keep int, bias Bias) []int {
	groups := keep + 1
	buckets := k + 1
	base := buckets / groups
	rem := buckets % groups

	idx := make([]int, 0, keep)
	cum := 0
	for g := 0; g < groups-1; g++ {
		size := base
		// Distribute the `rem` larger groups to the low end for BiasUpper and to
		// the high end for BiasLower.
		if bias == BiasLower {
			if g >= groups-rem {
				size++
			}
		} else if g < rem {
			size++
		}
		cum += size
		idx = append(idx, cum-1) // bound between bucket cum-1 and cum
	}
	return idx
}

// selectByValue keeps the bounds nearest to `keep` evenly spaced targets across
// the value range (log-transformed when logSpace is set). Selection is greedy
// left-to-right and always yields `keep` distinct, increasing indices.
func selectByValue(bounds []float64, keep int, bias Bias, logSpace bool) []int {
	k := len(bounds)
	if keep >= k {
		return identity(k)
	}

	pos := make([]float64, k)
	if logSpace && positive(bounds) {
		for i, b := range bounds {
			pos[i] = math.Log(b)
		}
	} else {
		copy(pos, bounds)
	}
	lo, hi := pos[0], pos[k-1]

	idx := make([]int, 0, keep)
	used := 0
	for i := 0; i < keep; i++ {
		var target float64
		if keep == 1 {
			target = (lo + hi) / 2
		} else {
			target = lo + float64(i)*(hi-lo)/float64(keep-1)
		}
		// Leave enough candidates for the remaining targets.
		maxJ := k - (keep - i)
		best := used
		bestDist := math.Abs(pos[used] - target)
		for j := used + 1; j <= maxJ; j++ {
			d := math.Abs(pos[j] - target)
			// On a tie, BiasUpper prefers the larger index (finer at the top).
			if d < bestDist || (d == bestDist && bias == BiasUpper) {
				best, bestDist = j, d
			}
		}
		idx = append(idx, best)
		used = best + 1
	}
	return idx
}

// applyRetain forces every present pin into the selection. When that pushes the
// count over `keep`, it drops the auto-selected (non-pinned) bounds closest to a
// pin first; if the pins alone still exceed `keep`, they all survive and the
// budget is exceeded.
func applyRetain(bounds []float64, selected []int, keep int, retain []float64) []int {
	if len(retain) == 0 {
		return selected
	}
	pins := pinIndices(bounds, retain)
	if len(pins) == 0 {
		return selected
	}

	pinned := make(map[int]bool, len(pins))
	for _, p := range pins {
		pinned[p] = true
	}

	// Non-pinned selections, ranked by distance to the nearest pin so the most
	// redundant ones are dropped first.
	var nonPin []int
	for _, s := range selected {
		if !pinned[s] {
			nonPin = append(nonPin, s)
		}
	}
	sort.Slice(nonPin, func(a, b int) bool {
		da, db := nearestPinDist(nonPin[a], pins), nearestPinDist(nonPin[b], pins)
		if da != db {
			return da < db
		}
		return nonPin[a] < nonPin[b]
	})

	kept := make(map[int]bool, len(pins)+len(nonPin))
	for _, p := range pins {
		kept[p] = true
	}
	// Add non-pinned selections (farthest-from-pin first) until the budget is full.
	for i := len(nonPin) - 1; i >= 0 && len(kept) < keep; i-- {
		kept[nonPin[i]] = true
	}

	out := make([]int, 0, len(kept))
	for idx := range kept {
		out = append(out, idx)
	}
	sort.Ints(out)
	return out
}

// pinIndices returns the sorted, de-duplicated bound indices whose value matches
// one of the retained values exactly.
func pinIndices(bounds, retain []float64) []int {
	want := make(map[float64]bool, len(retain))
	for _, r := range retain {
		want[r] = true
	}
	var idx []int
	for i, b := range bounds {
		if want[b] {
			idx = append(idx, i)
		}
	}
	return idx
}

func nearestPinDist(idx int, pins []int) int {
	best := math.MaxInt
	for _, p := range pins {
		if d := abs(idx - p); d < best {
			best = d
		}
	}
	return best
}

func positive(bounds []float64) bool {
	for _, b := range bounds {
		if b <= 0 {
			return false
		}
	}
	return true
}

func identity(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	return idx
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
