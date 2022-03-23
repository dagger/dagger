// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package env // import "go.opentelemetry.io/otel/sdk/internal/env"

import (
	"os"
	"strconv"

	"go.opentelemetry.io/otel/internal/global"
)

// Environment variable names
const (
	// BatchSpanProcessorScheduleDelayKey
	// Delay interval between two consecutive exports.
	// i.e. 5000
	BatchSpanProcessorScheduleDelayKey = "OTEL_BSP_SCHEDULE_DELAY"
	// BatchSpanProcessorExportTimeoutKey
	// Maximum allowed time to export data.
	// i.e. 3000
	BatchSpanProcessorExportTimeoutKey = "OTEL_BSP_EXPORT_TIMEOUT"
	// BatchSpanProcessorMaxQueueSizeKey
	// Maximum queue size
	// i.e. 2048
	BatchSpanProcessorMaxQueueSizeKey = "OTEL_BSP_MAX_QUEUE_SIZE"
	// BatchSpanProcessorMaxExportBatchSizeKey
	// Maximum batch size
	// Note: Must be less than or equal to EnvBatchSpanProcessorMaxQueueSize
	// i.e. 512
	BatchSpanProcessorMaxExportBatchSizeKey = "OTEL_BSP_MAX_EXPORT_BATCH_SIZE"

	// SpanAttributeValueLengthKey
	// Maximum allowed attribute value size.
	SpanAttributeValueLengthKey = "OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT"

	// SpanAttributeCountKey
	// Maximum allowed span attribute count
	SpanAttributeCountKey = "OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT"

	// SpanEventCountKey
	// Maximum allowed span event count
	SpanEventCountKey = "OTEL_SPAN_EVENT_COUNT_LIMIT"

	// SpanEventAttributeCountKey
	// Maximum allowed attribute per span event count.
	SpanEventAttributeCountKey = "OTEL_EVENT_ATTRIBUTE_COUNT_LIMIT"

	// SpanLinkCountKey
	// Maximum allowed span link count
	SpanLinkCountKey = "OTEL_SPAN_LINK_COUNT_LIMIT"

	// SpanLinkAttributeCountKey
	// Maximum allowed attribute per span link count
	SpanLinkAttributeCountKey = "OTEL_LINK_ATTRIBUTE_COUNT_LIMIT"
)

// IntEnvOr returns the int value of the environment variable with name key if
// it exists and the value is an int. Otherwise, defaultValue is returned.
func IntEnvOr(key string, defaultValue int) int {
	value, ok := os.LookupEnv(key)
	if !ok {
		return defaultValue
	}

	intValue, err := strconv.Atoi(value)
	if err != nil {
		global.Info("Got invalid value, number value expected.", key, value)
		return defaultValue
	}

	return intValue
}

// BatchSpanProcessorScheduleDelay returns the environment variable value for
// the OTEL_BSP_SCHEDULE_DELAY key if it exists, otherwise defaultValue is
// returned.
func BatchSpanProcessorScheduleDelay(defaultValue int) int {
	return IntEnvOr(BatchSpanProcessorScheduleDelayKey, defaultValue)
}

// BatchSpanProcessorExportTimeout returns the environment variable value for
// the OTEL_BSP_EXPORT_TIMEOUT key if it exists, otherwise defaultValue is
// returned.
func BatchSpanProcessorExportTimeout(defaultValue int) int {
	return IntEnvOr(BatchSpanProcessorExportTimeoutKey, defaultValue)
}

// BatchSpanProcessorMaxQueueSize returns the environment variable value for
// the OTEL_BSP_MAX_QUEUE_SIZE key if it exists, otherwise defaultValue is
// returned.
func BatchSpanProcessorMaxQueueSize(defaultValue int) int {
	return IntEnvOr(BatchSpanProcessorMaxQueueSizeKey, defaultValue)
}

// BatchSpanProcessorMaxExportBatchSize returns the environment variable value for
// the OTEL_BSP_MAX_EXPORT_BATCH_SIZE key if it exists, otherwise defaultValue
// is returned.
func BatchSpanProcessorMaxExportBatchSize(defaultValue int) int {
	return IntEnvOr(BatchSpanProcessorMaxExportBatchSizeKey, defaultValue)
}

// SpanAttributeValueLength returns the environment variable value for the
// OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT key if it exists, otherwise
// defaultValue is returned.
func SpanAttributeValueLength(defaultValue int) int {
	return IntEnvOr(SpanAttributeValueLengthKey, defaultValue)
}

// SpanAttributeCount returns the environment variable value for the
// OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT key if it exists, otherwise defaultValue is
// returned.
func SpanAttributeCount(defaultValue int) int {
	return IntEnvOr(SpanAttributeCountKey, defaultValue)
}

// SpanEventCount returns the environment variable value for the
// OTEL_SPAN_EVENT_COUNT_LIMIT key if it exists, otherwise defaultValue is
// returned.
func SpanEventCount(defaultValue int) int {
	return IntEnvOr(SpanEventCountKey, defaultValue)
}

// SpanEventAttributeCount returns the environment variable value for the
// OTEL_EVENT_ATTRIBUTE_COUNT_LIMIT key if it exists, otherwise defaultValue
// is returned.
func SpanEventAttributeCount(defaultValue int) int {
	return IntEnvOr(SpanEventAttributeCountKey, defaultValue)
}

// SpanLinkCount returns the environment variable value for the
// OTEL_SPAN_LINK_COUNT_LIMIT key if it exists, otherwise defaultValue is
// returned.
func SpanLinkCount(defaultValue int) int {
	return IntEnvOr(SpanLinkCountKey, defaultValue)
}

// SpanLinkAttributeCount returns the environment variable value for the
// OTEL_LINK_ATTRIBUTE_COUNT_LIMIT key if it exists, otherwise defaultValue is
// returned.
func SpanLinkAttributeCount(defaultValue int) int {
	return IntEnvOr(SpanLinkAttributeCountKey, defaultValue)
}
