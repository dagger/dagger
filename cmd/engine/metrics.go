package main

import (
	"net/http"

	"github.com/dagger/dagger/engine/server"
	"github.com/dagger/dagger/engine/slog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	connectedClientsGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "dagger_connected_clients",
		Help: "Number of currently connected clients",
	})
)

// setupMetricsServer starts an HTTP server to expose Prometheus metrics
func setupMetricsServer(srv *server.Server, addr string) error {
	if err := prometheus.Register(connectedClientsGauge); err != nil {
		return err
	}

	// Set up HTTP server
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		// Update the gauge with the current number of connected clients
		connectedClientsGauge.Set(float64(srv.ConnectedClients()))
		promhttp.Handler().ServeHTTP(w, r)
	})

	// Start server in a goroutine
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			slog.Error("metrics server failed", "error", err)
		}
	}()

	slog.Info("metrics server started", "address", addr)
	return nil
}
