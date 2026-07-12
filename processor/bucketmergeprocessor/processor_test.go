// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package bucketmergeprocessor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/pdatatest/pmetrictest"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/bucketmergeprocessor/internal/metadata"
)

// run feeds md through a processor built from cfg and returns the mutated result.
func run(t *testing.T, cfg *Config, md pmetric.Metrics) pmetric.Metrics {
	t.Helper()
	sink := &consumertest.MetricsSink{}
	proc, err := NewFactory().CreateMetrics(
		context.Background(),
		processortest.NewNopSettings(metadata.Type),
		cfg,
		sink,
	)
	require.NoError(t, err)
	require.NoError(t, proc.ConsumeMetrics(context.Background(), md))
	got := sink.AllMetrics()
	require.Len(t, got, 1)
	return got[0]
}

// classicMetric builds a single-metric payload holding one classic histogram.
func classicMetric(bounds []float64, counts []uint64) pmetric.Metrics {
	md := pmetric.NewMetrics()
	m := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("latency")
	h := m.SetEmptyHistogram()
	h.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	dp := h.DataPoints().AppendEmpty()
	dp.ExplicitBounds().FromRaw(bounds)
	dp.BucketCounts().FromRaw(counts)
	var total uint64
	for _, c := range counts {
		total += c
	}
	dp.SetCount(total)
	dp.SetSum(42)
	return md
}

// expoMetric builds a single-metric payload holding one exponential histogram.
func expoMetric(scale, posOffset int32, pos []uint64) pmetric.Metrics {
	md := pmetric.NewMetrics()
	m := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("latency")
	h := m.SetEmptyExponentialHistogram()
	h.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	dp := h.DataPoints().AppendEmpty()
	dp.SetScale(scale)
	dp.Positive().SetOffset(posOffset)
	dp.Positive().BucketCounts().FromRaw(pos)
	var total uint64
	for _, c := range pos {
		total += c
	}
	dp.SetCount(total)
	return md
}

func gaugeMetric() pmetric.Metrics {
	md := pmetric.NewMetrics()
	m := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("temperature")
	m.SetEmptyGauge().DataPoints().AppendEmpty().SetDoubleValue(3.14)
	return md
}

func TestClassicReduced(t *testing.T) {
	cfg := &Config{Classic: &ClassicConfig{MaxBuckets: 3, Strategy: StrategyCount}}
	got := run(t, cfg, classicMetric([]float64{1, 2, 3, 4, 5, 6}, []uint64{1, 2, 3, 4, 5, 6, 7}))

	want := classicMetric([]float64{3, 5}, []uint64{6, 9, 13})
	require.NoError(t, pmetrictest.CompareMetrics(want, got))
}

func TestClassicPassThroughWhenUnconfigured(t *testing.T) {
	// Only exponential configured: classic histograms must be untouched.
	cfg := &Config{Exponential: &ExponentialConfig{MaxBuckets: 2}}
	in := classicMetric([]float64{1, 2, 3, 4, 5, 6}, []uint64{1, 2, 3, 4, 5, 6, 7})
	got := run(t, cfg, classicMetric([]float64{1, 2, 3, 4, 5, 6}, []uint64{1, 2, 3, 4, 5, 6, 7}))
	require.NoError(t, pmetrictest.CompareMetrics(in, got))
}

func TestExponentialReduced(t *testing.T) {
	cfg := &Config{Exponential: &ExponentialConfig{MaxBuckets: 3}}
	// 8 populated positive buckets at scale 4 → must collapse to span ≤ 3.
	got := run(t, cfg, expoMetric(4, 0, []uint64{1, 1, 1, 1, 1, 1, 1, 1}))

	dp := got.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).ExponentialHistogram().DataPoints().At(0)
	require.LessOrEqual(t, dp.Positive().BucketCounts().Len(), 3, "span within limit")
	require.Less(t, dp.Scale(), int32(4), "scale lowered")
	require.Equal(t, uint64(8), dp.Count(), "count preserved")
	var got8 uint64
	for i := 0; i < dp.Positive().BucketCounts().Len(); i++ {
		got8 += dp.Positive().BucketCounts().At(i)
	}
	require.Equal(t, uint64(8), got8, "bucket total preserved")
}

func TestExponentialPassThroughWhenUnconfigured(t *testing.T) {
	cfg := &Config{Classic: &ClassicConfig{MaxBuckets: 2}}
	in := expoMetric(4, 0, []uint64{1, 1, 1, 1, 1, 1, 1, 1})
	got := run(t, cfg, expoMetric(4, 0, []uint64{1, 1, 1, 1, 1, 1, 1, 1}))
	require.NoError(t, pmetrictest.CompareMetrics(in, got))
}

func TestNonHistogramUntouched(t *testing.T) {
	cfg := &Config{
		Classic:     &ClassicConfig{MaxBuckets: 2},
		Exponential: &ExponentialConfig{MaxBuckets: 2},
	}
	got := run(t, cfg, gaugeMetric())
	require.NoError(t, pmetrictest.CompareMetrics(gaugeMetric(), got))
}

func TestAllNilIsPassThrough(t *testing.T) {
	cfg := &Config{}
	got := run(t, cfg, classicMetric([]float64{1, 2, 3, 4}, []uint64{1, 1, 1, 1, 1}))
	require.NoError(t, pmetrictest.CompareMetrics(
		classicMetric([]float64{1, 2, 3, 4}, []uint64{1, 1, 1, 1, 1}), got))
}

func TestBothTypesInOnePayload(t *testing.T) {
	cfg := &Config{
		Classic:     &ClassicConfig{MaxBuckets: 3, Strategy: StrategyCount},
		Exponential: &ExponentialConfig{MaxBuckets: 3},
	}
	md := classicMetric([]float64{1, 2, 3, 4, 5, 6}, []uint64{1, 2, 3, 4, 5, 6, 7})
	// add an exponential histogram alongside the classic one
	sm := md.ResourceMetrics().At(0).ScopeMetrics().At(0)
	em := sm.Metrics().AppendEmpty()
	em.SetName("size")
	eh := em.SetEmptyExponentialHistogram()
	eh.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	edp := eh.DataPoints().AppendEmpty()
	edp.SetScale(3)
	edp.Positive().BucketCounts().FromRaw([]uint64{1, 1, 1, 1, 1, 1, 1, 1})
	edp.SetCount(8)

	got := run(t, cfg, md)
	metrics := got.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	require.Equal(t, 2, metrics.Len())

	classicDP := metrics.At(0).Histogram().DataPoints().At(0)
	require.Equal(t, []uint64{6, 9, 13}, classicDP.BucketCounts().AsRaw())

	expoDP := metrics.At(1).ExponentialHistogram().DataPoints().At(0)
	require.LessOrEqual(t, expoDP.Positive().BucketCounts().Len(), 3)
}
