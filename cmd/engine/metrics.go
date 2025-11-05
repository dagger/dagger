package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/dagger/dagger/engine/server"
	"github.com/dagger/dagger/engine/slog"
)

var (
	connectedClientsGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "dagger_connected_clients",
		Help: "Number of currently connected clients",
	})

	localCacheTotalDiskSizeGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "dagger_local_cache_total_disk_size_bytes",
		Help: "Total disk space consumed by the local cache in bytes",
	})

	localCacheEntriesGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "dagger_local_cache_entries",
		Help: "Number of entries in the local cache",
	})

	localCacheCorruptDBResetGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "dagger_local_cache_corrupt_db_reset",
		Help: "If set, the local cache database was found to be corrupt and reset",
	})
)

// setupMetricsServer starts an HTTP server to expose Prometheus metrics
func setupMetricsServer(ctx context.Context, srv *server.Server, addr string) error {
	if err := prometheus.Register(connectedClientsGauge); err != nil {
		return err
	}
	if err := prometheus.Register(localCacheTotalDiskSizeGauge); err != nil {
		return err
	}
	if err := prometheus.Register(localCacheEntriesGauge); err != nil {
		return err
	}
	if err := prometheus.Register(localCacheCorruptDBResetGauge); err != nil {
		return err
	}

	// Only update local cache metrics at most every 5 minutes to avoid excessive holding
	// of buildkit's DiskUsage lock.
	// Support an override of the default 5 minute interval; mainly used now so integ tests
	// don't have to wait 5 minutes for the metrics to be updated.
	cacheMetricsInterval := 5 * time.Minute
	if intervalStr, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_METRICS_CACHE_UPDATE_INTERVAL"); ok {
		if interval, err := time.ParseDuration(intervalStr); err == nil {
			cacheMetricsInterval = interval
		} else {
			slog.Warn("invalid _EXPERIMENTAL_DAGGER_METRICS_CACHE_UPDATE_INTERVAL value, using default 5 minutes", "error", err)
		}
	}
	go func() {
		updateMetrics := func() {
			entrySet, err := srv.EngineLocalCacheEntries(ctx)
			if err == nil {
				localCacheTotalDiskSizeGauge.Set(float64(entrySet.DiskSpaceBytes))
				localCacheEntriesGauge.Set(float64(entrySet.EntryCount))
			} else {
				slog.Error("failed to get local cache entries for prometheus metrics", "error", err)
			}
		}

		// do an initial update immediately
		updateMetrics()

		ticker := time.NewTicker(cacheMetricsInterval)
		for {
			select {
			case <-ctx.Done():
				slog.Info("metrics server context done, stopping cache metrics updates")
				return
			case <-ticker.C:
				updateMetrics()
			}
		}
	}()

	// Set up HTTP server
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		connectedClientsGauge.Set(float64(srv.ConnectedClients()))

		var dbReset float64
		if srv.CorruptDBReset() {
			dbReset = 1
		}
		localCacheCorruptDBResetGauge.Set(dbReset)

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
