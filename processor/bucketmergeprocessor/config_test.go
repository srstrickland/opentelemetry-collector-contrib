// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package bucketmergeprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name: "both-nil-passthrough",
			cfg:  &Config{},
		},
		{
			name: "classic-only",
			cfg:  &Config{Classic: &ClassicConfig{MaxBuckets: 10, Strategy: StrategyLogarithmic}},
		},
		{
			name: "classic-count-with-bias",
			cfg:  &Config{Classic: &ClassicConfig{MaxBuckets: 10, Strategy: StrategyCount, Bias: BiasLower}},
		},
		{
			name: "classic-linear-with-retain",
			cfg:  &Config{Classic: &ClassicConfig{MaxBuckets: 10, Strategy: StrategyLinear, RetainBounds: []float64{10, 30, 60}}},
		},
		{
			name: "exponential-only",
			cfg:  &Config{Exponential: &ExponentialConfig{MaxBuckets: 20}},
		},
		{
			name: "both",
			cfg: &Config{
				Classic:     &ClassicConfig{MaxBuckets: 10},
				Exponential: &ExponentialConfig{MaxBuckets: 20},
			},
		},
		{
			name: "classic-empty-strategy-defaults-ok",
			cfg:  &Config{Classic: &ClassicConfig{MaxBuckets: 5}},
		},
		{
			name:    "classic-max-buckets-too-low",
			cfg:     &Config{Classic: &ClassicConfig{MaxBuckets: 1}},
			wantErr: "classic: max_buckets must be >= 2",
		},
		{
			name:    "classic-unknown-strategy",
			cfg:     &Config{Classic: &ClassicConfig{MaxBuckets: 10, Strategy: "bogus"}},
			wantErr: `classic: unknown strategy "bogus"`,
		},
		{
			name:    "classic-unknown-bias",
			cfg:     &Config{Classic: &ClassicConfig{MaxBuckets: 10, Bias: "sideways"}},
			wantErr: `classic: unknown bias "sideways"`,
		},
		{
			name:    "exponential-max-buckets-too-low",
			cfg:     &Config{Exponential: &ExponentialConfig{MaxBuckets: 0}},
			wantErr: "exponential: max_buckets must be >= 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDefaults(t *testing.T) {
	assert.Equal(t, StrategyLogarithmic, defaultStrategy)
	assert.Equal(t, BiasUpper, defaultBias)
}
