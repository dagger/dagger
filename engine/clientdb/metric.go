package clientdb

import (
	"log/slog"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func MetricsToPB(dbMetrics []Metric) []*otlpmetricsv1.ResourceMetrics {
	if len(dbMetrics) == 0 {
		return nil
	}

	resourceMetrics := make(map[attribute.Distinct]*otlpmetricsv1.ResourceMetrics)

	type key struct {
		r  attribute.Distinct
		is instrumentation.Scope
	}
	scopeMetrics := make(map[key]*otlpmetricsv1.ScopeMetrics)

	for _, dbMetric := range dbMetrics {
		var metricResourcePB otlpmetricsv1.ResourceMetrics
		if err := protojson.Unmarshal(dbMetric.Data, &metricResourcePB); err != nil {
			slog.Error("failed to unmarshal metric resource", "error", err, "metric", dbMetric)
			continue
		}

		res := telemetry.ResourceFromPB(metricResourcePB.SchemaUrl, metricResourcePB.Resource)

		for _, scopeMetricPB := range metricResourcePB.ScopeMetrics {
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
