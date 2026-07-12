// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:generate make mdatagen

// package bucketmergeprocessor implements a processor that reduces the number of
// buckets in histogram metrics, for both classic (explicit-bounds) and
// exponential histograms, using independently configurable limits.
package bucketmergeprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/bucketmergeprocessor"
