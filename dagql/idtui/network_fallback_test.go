package idtui

import (
	"bytes"
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestNetworkLegacyFallback(t *testing.T) {
	for _, tc := range []struct {
		name                string
		values              map[string]int64
		useNetwork          bool
		legacy, unavailable bool
	}{
		{"legacy only", map[string]int64{telemetry.NetstatRxBytes: 1024}, false, true, false},
		{"prefer network", map[string]int64{telemetry.NetstatRxBytes: 1024, telemetryattrs.NetworkRxBytes: 512}, true, false, false},
		{"zero is valid", map[string]int64{telemetry.NetstatRxBytes: 1024, telemetryattrs.NetworkRxBytes: 0}, true, false, false},
		{"available without samples", map[string]int64{telemetry.NetstatRxBytes: 1024, telemetryattrs.NetworkAvailable: 1}, true, false, false},
		{"unavailable with fallback", map[string]int64{telemetry.NetstatRxBytes: 1024, telemetryattrs.NetworkAvailable: 0}, false, true, false},
		{"unavailable without fallback", map[string]int64{telemetryattrs.NetworkAvailable: 0}, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metrics := map[string][]metricdata.DataPoint[int64]{}
			for name, value := range tc.values {
				metrics[name] = []metricdata.DataPoint[int64]{{Value: value}}
			}
			r := renderer{}
			r.Verbosity = 3
			var buf bytes.Buffer
			useNetwork := r.renderNetworkAvailability(NewOutput(&buf, termenv.WithProfile(termenv.Ascii)), metrics)
			require.Equal(t, tc.useNetwork, useNetwork)
			if tc.legacy {
				require.Contains(t, buf.String(), "Network Rx")
			} else {
				require.NotContains(t, buf.String(), "Network Rx")
			}
			if tc.unavailable {
				require.Contains(t, buf.String(), "unavailable")
			} else {
				require.NotContains(t, buf.String(), "unavailable")
			}
		})
	}
}
