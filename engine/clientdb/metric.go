package clientdb

import (
	"bytes"
	"log/slog"
	"slices"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func MetricsToPB(dbMetrics []Metric) []*otlpmetricsv1.ResourceMetrics {
	return NamedMetricsToPB(dbMetrics, nil)
}

// NamedMetricsToPB is MetricsToPB keeping only the metrics whose names are in
// names; nil keeps every metric. A row whose encoding holds none of the names'
// bytes is skipped without decoding it: an archive's metric stream is mostly
// per-exec resource metrics a name selection leaves out.
func NamedMetricsToPB(dbMetrics []Metric, names map[string]bool) []*otlpmetricsv1.ResourceMetrics {
	if len(dbMetrics) == 0 {
		return nil
	}
	if names != nil {
		dbMetrics = slices.DeleteFunc(slices.Clone(dbMetrics), func(m Metric) bool {
			for name := range names {
				if bytes.Contains(m.Data, []byte(name)) {
					return false
				}
			}
			return true
		})
	}

	resourceMetrics := make(map[attribute.Distinct]*otlpmetricsv1.ResourceMetrics)

	type key struct {
		r  attribute.Distinct
		is instrumentation.Scope
	}
	scopeMetrics := make(map[key]*otlpmetricsv1.ScopeMetrics)

	for _, dbMetric := range dbMetrics {
		var metricResourcePB otlpmetricsv1.ResourceMetrics
		if err := proto.Unmarshal(dbMetric.Data, &metricResourcePB); err != nil {
			slog.Error("failed to unmarshal metric resource", "error", err, "metric", dbMetric)
			continue
		}

		res := telemetry.ResourceFromPB(metricResourcePB.SchemaUrl, metricResourcePB.Resource)

		for _, scopeMetricPB := range metricResourcePB.ScopeMetrics {
			if names != nil {
				scopeMetricPB.Metrics = slices.DeleteFunc(scopeMetricPB.Metrics, func(m *otlpmetricsv1.Metric) bool {
					return !names[m.GetName()]
				})
				if len(scopeMetricPB.Metrics) == 0 {
					continue
				}
			}
			scope := telemetry.InstrumentationScopeFromPB(scopeMetricPB.Scope)

			rKey := res.Equivalent()
			k := key{
				r:  rKey,
				is: scope,
			}
			scopeMetric, iOk := scopeMetrics[k]
			if !iOk {
				// Either the resource or instrumentation scope were unknown.
				scopeMetric = &otlpmetricsv1.ScopeMetrics{
					Scope:     scopeMetricPB.Scope,
					Metrics:   []*otlpmetricsv1.Metric{},
					SchemaUrl: scope.SchemaURL,
				}
			}
			scopeMetric.Metrics = append(scopeMetric.Metrics, scopeMetricPB.Metrics...)
			scopeMetrics[k] = scopeMetric

			rs, rOk := resourceMetrics[rKey]
			if !rOk {
				rs = &otlpmetricsv1.ResourceMetrics{
					Resource:     metricResourcePB.Resource,
					ScopeMetrics: []*otlpmetricsv1.ScopeMetrics{scopeMetric},
					SchemaUrl:    res.SchemaURL(),
				}
				resourceMetrics[rKey] = rs
				continue
			}
			if !iOk {
				rs.ScopeMetrics = append(rs.ScopeMetrics, scopeMetric)
			}
		}
	}

	results := make([]*otlpmetricsv1.ResourceMetrics, 0, len(resourceMetrics))
	for _, rm := range resourceMetrics {
		results = append(results, rm)
	}
	return results
}
