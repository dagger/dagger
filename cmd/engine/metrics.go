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

	localCacheTotalDiskSizeGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "dagger_local_cache_total_disk_size_bytes",
		Help: "Total disk space consumed by the local cache in bytes. Will be -1 if an error occurs.",
	})

	localCacheEntryCountGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "dagger_local_cache_entry_count",
		Help: "Number of entries in the local cache. Will be -1 if an error occurs.",
	})
)

// setupMetricsServer starts an HTTP server to expose Prometheus metrics
func setupMetricsServer(srv *server.Server, addr string) error {
	if err := prometheus.Register(connectedClientsGauge); err != nil {
		return err
	}
	if err := prometheus.Register(localCacheTotalDiskSizeGauge); err != nil {
		return err
	}
	if err := prometheus.Register(localCacheEntryCountGauge); err != nil {
		return err
	}

	// Set up HTTP server
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		connectedClientsGauge.Set(float64(srv.ConnectedClients()))

		entrySet, err := srv.EngineLocalCacheEntries(r.Context())
		if err == nil {
			localCacheTotalDiskSizeGauge.Set(float64(entrySet.DiskSpaceBytes))
			localCacheEntryCountGauge.Set(float64(entrySet.EntryCount))
		} else {
			slog.Error("failed to get local cache entries for prometheus metrics", "error", err)
			// set to -1 to indicate an error
			localCacheTotalDiskSizeGauge.Set(-1)
			localCacheEntryCountGauge.Set(-1)
		}

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
