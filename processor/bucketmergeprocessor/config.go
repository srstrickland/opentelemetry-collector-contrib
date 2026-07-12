// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package bucketmergeprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/bucketmergeprocessor"

import (
	"fmt"

	"go.opentelemetry.io/collector/component"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/bucketmergeprocessor/internal/classic"
)

var _ component.Config = (*Config)(nil)

// Strategy and Bias are defined by the classic merge implementation and
// re-exported here so the user-facing config lives in one place.
type (
	// Strategy selects how classic histogram bounds are chosen when merging.
	Strategy = classic.Strategy
	// Bias selects which end keeps finer resolution on an uneven split.
	Bias = classic.Bias
)

const (
	// StrategyCount groups buckets by count (positional).
	StrategyCount = classic.StrategyCount
	// StrategyLinear keeps bounds evenly spaced on a linear axis.
	StrategyLinear = classic.StrategyLinear
	// StrategyLogarithmic keeps bounds evenly spaced on a log axis. Default.
	StrategyLogarithmic = classic.StrategyLogarithmic

	// BiasLower keeps finer resolution at the low end.
	BiasLower = classic.BiasLower
	// BiasUpper keeps finer resolution at the high end. Default.
	BiasUpper = classic.BiasUpper
)

// defaults applied when a classic config omits the field.
const (
	defaultStrategy = classic.DefaultStrategy
	defaultBias     = classic.DefaultBias
)

// minBuckets is the smallest limit that still yields a meaningful histogram:
// at least one finite bucket plus the implicit overflow region.
const minBuckets = 2

// Config defines the configuration for the bucketmerge processor.
//
// Both sections are optional. A histogram type whose section is omitted is
// passed through unchanged, so a configuration only needs to specify the
// histogram type(s) it wants to reduce.
type Config struct {
	// Classic configures merging of classic (explicit-bounds) histograms.
	// When nil, classic histograms are passed through unchanged.
	Classic *ClassicConfig `mapstructure:"classic"`
	// Exponential configures merging of exponential histograms.
	// When nil, exponential histograms are passed through unchanged.
	Exponential *ExponentialConfig `mapstructure:"exponential"`
}

// ClassicConfig configures reduction of classic (explicit-bounds) histograms.
type ClassicConfig struct {
	// MaxBuckets is the maximum number of bucket counts retained per data point,
	// including the final +Inf overflow bucket. Data points already at or below
	// this many buckets are left unchanged.
	MaxBuckets int `mapstructure:"max_buckets"`
	// Strategy selects how surviving bounds are chosen: "count", "linear" or
	// "logarithmic". Defaults to "logarithmic".
	Strategy Strategy `mapstructure:"strategy"`
	// Bias breaks ties on an uneven split: "lower" (finer at the low end) or
	// "upper" (finer at the tail). Defaults to "upper".
	Bias Bias `mapstructure:"bias"`
	// RetainBounds lists bound values that must survive the merge if present in
	// the input (e.g. SLA thresholds). They take priority over MaxBuckets; if
	// they alone exceed it, MaxBuckets becomes best-effort.
	RetainBounds []float64 `mapstructure:"retain_bounds"`
}

// ExponentialConfig configures reduction of exponential histograms.
type ExponentialConfig struct {
	// MaxBuckets is the maximum number of populated buckets retained per sign
	// (positive and negative) per data point. The scale is lowered (merging
	// adjacent buckets) until the populated span fits within this limit.
	MaxBuckets int `mapstructure:"max_buckets"`
}

// Validate checks the configuration for invalid values.
func (c *Config) Validate() error {
	if c.Classic != nil {
		if c.Classic.MaxBuckets < minBuckets {
			return fmt.Errorf("classic: max_buckets must be >= %d, got %d", minBuckets, c.Classic.MaxBuckets)
		}
		switch c.Classic.Strategy {
		case "", StrategyCount, StrategyLinear, StrategyLogarithmic:
		default:
			return fmt.Errorf("classic: unknown strategy %q (supported: %q, %q, %q)",
				c.Classic.Strategy, StrategyCount, StrategyLinear, StrategyLogarithmic)
		}
		switch c.Classic.Bias {
		case "", BiasLower, BiasUpper:
		default:
			return fmt.Errorf("classic: unknown bias %q (supported: %q, %q)",
				c.Classic.Bias, BiasLower, BiasUpper)
		}
	}
	if c.Exponential != nil {
		if c.Exponential.MaxBuckets < minBuckets {
			return fmt.Errorf("exponential: max_buckets must be >= %d, got %d", minBuckets, c.Exponential.MaxBuckets)
		}
	}
	return nil
}
