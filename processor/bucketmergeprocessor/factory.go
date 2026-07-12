// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package bucketmergeprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/bucketmergeprocessor"

import (
	"context"
	"errors"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processorhelper"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/bucketmergeprocessor/internal/metadata"
)

// NewFactory returns a new factory for the bucketmerge processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		metadata.Type,
		createDefaultConfig,
		processor.WithMetrics(createMetricsProcessor, metadata.MetricsStability))
}

func createDefaultConfig() component.Config {
	return &Config{}
}

func createMetricsProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Metrics,
) (processor.Metrics, error) {
	pCfg, ok := cfg.(*Config)
	if !ok {
		return nil, errors.New("configuration parsing error")
	}

	proc := newProcessor(pCfg, set.Logger)
	return processorhelper.NewMetrics(
		ctx,
		set,
		cfg,
		nextConsumer,
		proc.processMetrics,
		processorhelper.WithCapabilities(consumer.Capabilities{MutatesData: true}),
	)
}
