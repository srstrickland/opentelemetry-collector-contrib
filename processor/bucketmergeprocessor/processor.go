// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package bucketmergeprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/bucketmergeprocessor"

import (
	"context"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/bucketmergeprocessor/internal/classic"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/bucketmergeprocessor/internal/expo"
)

type bucketMergeProcessor struct {
	classic     *ClassicConfig
	exponential *ExponentialConfig
	logger      *zap.Logger
}

func newProcessor(cfg *Config, logger *zap.Logger) *bucketMergeProcessor {
	return &bucketMergeProcessor{
		classic:     cfg.Classic,
		exponential: cfg.Exponential,
		logger:      logger,
	}
}

// processMetrics reduces the bucket count of every histogram data point whose
// type has a configured limit, leaving all other metrics untouched.
func (p *bucketMergeProcessor) processMetrics(_ context.Context, md pmetric.Metrics) (pmetric.Metrics, error) {
	// Nothing configured: pass through without walking the payload.
	if p.classic == nil && p.exponential == nil {
		return md, nil
	}

	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		sms := rms.At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			metrics := sms.At(j).Metrics()
			for k := 0; k < metrics.Len(); k++ {
				p.processMetric(metrics.At(k))
			}
		}
	}
	return md, nil
}

func (p *bucketMergeProcessor) processMetric(m pmetric.Metric) {
	switch m.Type() {
	case pmetric.MetricTypeHistogram:
		if p.classic == nil {
			return
		}
		opts := classic.Options{
			MaxBuckets: p.classic.MaxBuckets,
			Strategy:   p.classic.Strategy,
			Bias:       p.classic.Bias,
			Retain:     p.classic.RetainBounds,
		}
		dps := m.Histogram().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			classic.Merge(dps.At(i), opts)
		}
	case pmetric.MetricTypeExponentialHistogram:
		if p.exponential == nil {
			return
		}
		dps := m.ExponentialHistogram().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			expo.Limit(dps.At(i), p.exponential.MaxBuckets)
		}
	}
}
