package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/exp/trace"
)

type flightRecorder struct {
	mu       sync.Mutex
	recorder *trace.FlightRecorder
}

func newFlightRecorder() *flightRecorder {
	dbg := &flightRecorder{
		recorder: trace.NewFlightRecorder(),
	}
	return dbg
}

func (r *flightRecorder) StartTrace(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recorder.Enabled() {
		http.Error(w, "flight recorder is already running", http.StatusConflict)
		return
	}
	if err := r.recorder.Start(); err != nil {
		http.Error(w, fmt.Sprintf("could not start flight recorder: %s", err), http.StatusInternalServerError)
		return
	}
}

func (r *flightRecorder) StopTrace(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recorder.Enabled() {
		http.Error(w, "flight recorder is not running", http.StatusConflict)
		return
	}
	if err := r.recorder.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (r *flightRecorder) SetTracePeriod(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recorder.Enabled() {
		http.Error(w, "flight recorder is running, stop it to change its period", http.StatusPreconditionFailed)
		return
	}
	periodValue := req.FormValue("period")
	period, err := time.ParseDuration(periodValue)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid flight recorder period: %s", err), http.StatusBadRequest)
	}
	r.recorder.SetPeriod(period)
}

func (r *flightRecorder) Trace(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="trace"`)
	if _, err := r.recorder.WriteTo(w); err != nil {
		http.Error(w, fmt.Sprintf("could not write in-flight trace: %s", err), http.StatusInternalServerError)
	}
}

func setupDebugFlight(m *http.ServeMux) {
	r := newFlightRecorder()

	const (
		flightPattern      = "/debug/flight"
		flightTracePattern = flightPattern + "/trace"
	)

	m.HandleFunc("POST "+flightTracePattern+"/start", r.StartTrace)
	m.HandleFunc("POST "+flightTracePattern+"/stop", r.StopTrace)
	m.HandleFunc("POST "+flightTracePattern+"/set_period", r.SetTracePeriod)
	m.HandleFunc("GET "+flightTracePattern, r.Trace)
}
