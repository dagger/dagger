package resources

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// memory.stat reaches only the workload export, with the time of memory.current.
func TestMemoryStatOnlyInWorkloadExport(t *testing.T) {
	for _, tc := range []struct {
		name string
		stat string // empty: the cgroup has no memory.stat
		want map[string]int64
	}{
		{
			name: "memory.stat",
			stat: "anon 1000\nfile 1800\nkernel 200\nactive_anon 900\ninactive_anon 100\nactive_file 800\ninactive_file 1000\nfile_dirty 4\n",
			want: map[string]int64{
				telemetry.MemoryCurrentBytes:     3000,
				enginetel.MemoryAnonName:         1000,
				enginetel.MemoryInactiveFileName: 1000,
				"available memory.current":       1,
				"available memory.stat":          1,
			},
		},
		{
			name: "no memory.stat",
			want: map[string]int64{
				telemetry.MemoryCurrentBytes: 3000,
				"available memory.current":   1,
				"available memory.stat":      0,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cgroup := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(cgroup, memoryCurrentFile), []byte("3000\n"), 0o600))
			if tc.stat != "" {
				require.NoError(t, os.WriteFile(filepath.Join(cgroup, memoryStatFile), []byte(tc.stat), 0o600))
			}
			reader := sdkmetric.NewManualReader()
			readings := enginetel.NewWorkloadReadings(time.Second)
			ctx := enginetel.WithWorkloadReadings(t.Context(), readings)
			meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")
			sampler, err := newMemoryCurrentSampler(cgroup, enginetel.WorkloadReadingMeter(ctx, meter, "test"), attribute.NewSet())
			require.NoError(t, err)
			require.NoError(t, sampler.sample(ctx))

			scopes, err := readings.Produce(ctx)
			require.NoError(t, err)
			got := map[string]int64{}
			times := map[time.Time]bool{}
			for _, scope := range scopes {
				for _, m := range scope.Metrics {
					for _, point := range m.Data.(metricdata.Gauge[int64]).DataPoints {
						name := m.Name
						if source, ok := point.Attributes.Value("source"); ok {
							name = "available " + source.AsString()
						}
						got[name] = point.Value
						times[point.Time] = true
					}
				}
			}
			require.Equal(t, tc.want, got)
			require.Len(t, times, 1, "the readings of one sample have one time")

			// The CLI and Cloud readers get memory.current only.
			var ordinary metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(ctx, &ordinary))
			var names []string
			for _, scope := range ordinary.ScopeMetrics {
				for _, m := range scope.Metrics {
					names = append(names, m.Name)
				}
			}
			require.Equal(t, []string{telemetry.MemoryCurrentBytes}, names)
		})
	}
}
