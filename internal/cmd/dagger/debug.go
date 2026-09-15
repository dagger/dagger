package daggercmd

import (
	"errors"
	"expvar"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"time"

	"golang.org/x/net/trace"
)

func debugHandlerMux() *http.ServeMux {
	m := http.NewServeMux()
	m.Handle("/debug/vars", expvar.Handler())
	m.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	m.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
	m.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	m.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	m.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	m.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	m.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	m.Handle("/debug/requests", http.HandlerFunc(trace.Traces))
	m.Handle("/debug/events", http.HandlerFunc(trace.Events))

	m.Handle("/debug/gc", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		runtime.GC()
		slog.Warn("triggered GC from debug endpoint")
	}))

	// setting debugaddr is opt-in. permission is defined by listener address
	trace.AuthRequest = func(_ *http.Request) (bool, bool) {
		return true, true
	}
	return m
}

func startDebugServer(addr string) (*http.Server, net.Listener, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	srv := &http.Server{
		Handler:           debugHandlerMux(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("debug handlers listening", "debugAddr", l.Addr().String())
	go func() {
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("debug handlers stopped", "error", err)
		}
	}()
	return srv, l, nil
}

func setupDebugHandlers(addr string) error {
	_, _, err := startDebugServer(addr)
	return err
}
