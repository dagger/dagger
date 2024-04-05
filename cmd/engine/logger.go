package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// some logs from buildkit/containerd libs are not useful even at debug level,
// this hook ignores them
type noiseReductionHook struct {
	ignoreLogger *logrus.Logger
}

var _ logrus.Hook = (*noiseReductionHook)(nil)

func (h *noiseReductionHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

var ignoredMessages = map[string]struct{}{
	"fetch":                        {},
	"fetch response received":      {},
	"resolving":                    {},
	"do request":                   {},
	"resolved":                     {},
	"push":                         {},
	"checking and pushing to":      {},
	"response completed":           {},
	"authorized request":           {},
	"serving grpc connection":      {},
	"diff applied":                 {},
	"using pigz for decompression": {},
}

var ignoredMessagePrefixes = []string{
	"returning network namespace",
	"releasing cni network namespace",
	"creating new network namespace",
	"finished creating network namespace",
	"finished setting up network namespace",
	"sending sigkill to process in container",
	"diffcopy took",
	"Using single walk diff for",
	"reusing ref for",
	"not reusing ref",
	"new ref for local",
}

func (h *noiseReductionHook) Fire(entry *logrus.Entry) error {
	var ignore bool
	if _, ok := ignoredMessages[entry.Message]; ok {
		ignore = true
	} else {
		for _, prefix := range ignoredMessagePrefixes {
			if strings.HasPrefix(entry.Message, prefix) {
				ignore = true
				break
			}
		}
	}
	if ignore {
		entry.Logger = h.ignoreLogger
	}
	return nil
}

type otelLogrusHook struct {
	rootSpan trace.Span
	logger   log.Logger
}

var _ logrus.Hook = (*otelLogrusHook)(nil)

func (h *otelLogrusHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *otelLogrusHook) Fire(entry *logrus.Entry) error {
	var rec log.Record
	rec.SetBody(log.StringValue(entry.Message))
	switch entry.Level {
	case logrus.PanicLevel:
		rec.SetSeverity(log.SeverityFatal)
	case logrus.FatalLevel:
		rec.SetSeverity(log.SeverityFatal)
	case logrus.ErrorLevel:
		rec.SetSeverity(log.SeverityError)
	case logrus.WarnLevel:
		rec.SetSeverity(log.SeverityWarn)
	case logrus.InfoLevel:
		rec.SetSeverity(log.SeverityInfo)
	case logrus.DebugLevel:
		rec.SetSeverity(log.SeverityDebug)
	case logrus.TraceLevel:
		rec.SetSeverity(log.SeverityTrace)
	}
	kvs := make([]log.KeyValue, 0, len(entry.Data))
	for key, val := range entry.Data {
		kvs = append(kvs, log.KeyValue{
			Key:   key,
			Value: logValue(val),
		})
	}
	rec.AddAttributes(kvs...)
	rec.SetTimestamp(entry.Time)

	// TODO revive if/when we want engine logs to correlate to a trace
	// ctx := entry.Context
	// if trace.SpanFromContext(entry.Context).SpanContext().IsValid() {
	// 	ctx = trace.ContextWithSpan(ctx, h.rootSpan)
	// }

	h.logger.Emit(entry.Context, rec)

	return nil
}

func logValue(v any) log.Value {
	switch x := v.(type) {
	case string:
		return log.StringValue(x)
	case []byte:
		return log.BytesValue(x)
	case float64:
		return log.Float64Value(x)
	case int:
		return log.IntValue(x)
	case int32:
		return log.Int64Value(int64(x))
	case int64:
		return log.Int64Value(x)
	case uint:
		return log.IntValue(int(x))
	case uint32:
		return log.Int64Value(int64(x))
	case uint64:
		return log.Int64Value(int64(x))
	case time.Duration:
		return log.Int64Value(x.Nanoseconds())
	case bool:
		return log.BoolValue(x)
	case map[string]any:
		kvs := make([]log.KeyValue, len(x))
		for key, val := range x {
			kvs = append(kvs, log.KeyValue{
				Key:   key,
				Value: logValue(val),
			})
		}
		return log.MapValue(kvs...)
	case []any:
		vals := make([]log.Value, len(x))
		for _, v := range x {
			vals = append(vals, logValue(v))
		}
		return log.SliceValue(vals...)
	default:
		// sane default fallback, don't want to panic
		return log.StringValue(fmt.Sprintf("%v", x))
	}
}
