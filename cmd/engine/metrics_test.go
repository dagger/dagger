package main

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestNetworkCollectorReportsAvailability(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(newNetworkCollector())
	families, err := registry.Gather()
	require.NoError(t, err)
	require.Len(t, families, 1)
	require.Equal(t, "dagger_network_accounting_available", families[0].GetName())
	require.Equal(t, float64(0), families[0].Metric[0].Gauge.GetValue())
}
