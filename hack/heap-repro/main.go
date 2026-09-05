// heap-repro drives one long-lived Dagger session through many module
// function calls while sampling the engine's heap profile, then renders the
// samples as a stacked area chart so the memory retained per nested client can
// be compared between engine builds.
//
// Every call spawns a fresh module runtime, which connects back to the engine
// as a nested client with its own telemetry. Before client reclamation those
// runtimes stayed alive until the session ended, so the heap grew with every
// call; after it, the heap stays flat.
//
// Usage, from a checkout whose dev engine is running (see hack/dev):
//
//	./hack/with-dev go run ./hack/heap-repro record -label main -out main.json
//	./hack/with-dev go run ./hack/heap-repro record -label branch -out branch.json
//	go run ./hack/heap-repro chart -out heap.html main.json branch.json
//
// hack/heap-repro/run.sh wraps the engine rebuild and the record step for one
// worktree.
package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dagger.io/dagger"
	"github.com/google/pprof/profile"
	"golang.org/x/sync/errgroup"
)

//go:embed testdata/workload
var workloadFS embed.FS

//go:embed chart.html
var chartTemplate string

// Run is one recorded timeline against one engine build.
type Run struct {
	Label         string    `json:"label"`
	EngineVersion string    `json:"engine_version"`
	RecordedAt    time.Time `json:"recorded_at"`
	Calls         int       `json:"calls"`
	Concurrency   int       `json:"concurrency"`
	Interval      string    `json:"interval"`
	Samples       []Sample  `json:"samples"`
	Events        []Event   `json:"events"`
}

// Sample is one heap profile, bucketed by allocation site.
type Sample struct {
	// T is seconds since the first measured call started.
	T float64 `json:"t"`
	// Phase is "calls" while module calls run, "idle" once they have all
	// completed but the session is still open, and "closed" after the session
	// has been closed.
	Phase      string `json:"phase"`
	CallsDone  int    `json:"calls_done"`
	HeapInuse  int64  `json:"heap_inuse"`
	Goroutines int    `json:"goroutines"`
	// Runtimes is the number of retained client runtimes reported by
	// /debug/client-lifecycle, absent on engines without that endpoint.
	Runtimes *int `json:"runtimes,omitempty"`
	// Sites maps an allocation site to the bytes in use that were allocated
	// there. The site is the innermost non-standard-library frame, which is
	// the closest thing a Go heap profile has to a type.
	Sites map[string]int64 `json:"sites"`
}

// Event marks a point on the timeline.
type Event struct {
	T    float64 `json:"t"`
	Kind string  `json:"kind"`
	N    int     `json:"n,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "record":
		err = record(os.Args[2:])
	case "chart":
		err = chart(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: heap-repro record [flags] | heap-repro chart [flags] run.json...")
}

func record(args []string) error {
	fl := flag.NewFlagSet("record", flag.ExitOnError)
	label := fl.String("label", "", "name for this run, shown on the chart")
	out := fl.String("out", "heap-repro.json", "where to write the recorded run")
	calls := fl.Int("calls", 100, "number of module function calls to make")
	concurrency := fl.Int("concurrency", 1, "module calls in flight at once")
	interval := fl.Duration("interval", 2*time.Second, "time between heap samples")
	tail := fl.Duration("tail", 20*time.Second, "how long to keep sampling after the calls finish, and again after the session closes")
	debugAddr := fl.String("debug", "http://localhost:6060", "engine debug endpoint")
	verbose := fl.Bool("verbose", false, "show the session's progress output")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *label == "" {
		return errors.New("-label is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	moduleDir, err := writeWorkload()
	if err != nil {
		return err
	}
	defer os.RemoveAll(moduleDir)

	logOut := io.Discard
	if *verbose {
		logOut = os.Stderr
	}
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(logOut))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			client.Close()
		}
	}()

	run := &Run{
		Label:       *label,
		RecordedAt:  time.Now(),
		Calls:       *calls,
		Concurrency: *concurrency,
		Interval:    interval.String(),
	}
	if err := client.Do(ctx, &dagger.Request{Query: `{ version }`}, &dagger.Response{Data: &struct {
		Version *string `json:"version"`
	}{Version: &run.EngineVersion}}); err != nil {
		return fmt.Errorf("query engine version: %w", err)
	}
	fmt.Fprintf(os.Stderr, "engine %s: serving workload module\n", run.EngineVersion)
	if err := client.ModuleSource(moduleDir).AsModule().Serve(ctx); err != nil {
		return fmt.Errorf("serve workload module: %w", err)
	}
	// The first call pays for the module runtime and image pulls; keep it out
	// of the measurement so both runs start from the same warmed state.
	if err := callHello(ctx, client, "warmup"); err != nil {
		return fmt.Errorf("warmup call: %w", err)
	}

	if _, _, err := fetchHeap(ctx, *debugAddr); err != nil {
		return fmt.Errorf("sample heap from %s: %w", *debugAddr, err)
	}

	s := &sampler{
		debugAddr: *debugAddr,
		start:     time.Now(),
		run:       run,
	}
	s.phase.Store("calls")
	if err := s.sample(ctx); err != nil {
		return err
	}
	sampleCtx, stopSampling := context.WithCancel(ctx)
	var sampling sync.WaitGroup
	sampling.Add(1)
	go func() {
		defer sampling.Done()
		s.loop(sampleCtx, *interval)
	}()

	fmt.Fprintf(os.Stderr, "running %d calls (%d at a time)\n", *calls, *concurrency)
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(*concurrency)
	for i := 1; i <= *calls; i++ {
		eg.Go(func() error {
			if err := callHello(egCtx, client, fmt.Sprintf("call-%d", i)); err != nil {
				return fmt.Errorf("call %d: %w", i, err)
			}
			n := int(s.callsDone.Add(1))
			s.event(Event{Kind: "call", N: n})
			if n%10 == 0 {
				fmt.Fprintf(os.Stderr, "  %d/%d calls done, heap %s\n", n, *calls, humanBytes(s.lastHeap.Load()))
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		stopSampling()
		sampling.Wait()
		return err
	}

	fmt.Fprintf(os.Stderr, "calls finished; sampling the idle session for %s\n", *tail)
	s.setPhase("idle")
	if err := sleepCtx(ctx, *tail); err != nil {
		stopSampling()
		sampling.Wait()
		return err
	}

	fmt.Fprintf(os.Stderr, "closing the session; sampling for another %s\n", *tail)
	closed = true
	if err := client.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "  close: %v\n", err)
	}
	s.setPhase("closed")
	if err := sleepCtx(ctx, *tail); err != nil {
		stopSampling()
		sampling.Wait()
		return err
	}
	stopSampling()
	sampling.Wait()
	if s.err != nil {
		return s.err
	}

	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d samples to %s\n", len(run.Samples), *out)
	return nil
}

func callHello(ctx context.Context, client *dagger.Client, seed string) error {
	var res struct {
		Workload struct {
			Hello string `json:"hello"`
		} `json:"workload"`
	}
	return client.Do(ctx, &dagger.Request{
		Query:     `query($seed: String!) { workload { hello(seed: $seed) } }`,
		Variables: map[string]any{"seed": seed},
	}, &dagger.Response{Data: &res})
}

// writeWorkload copies the embedded workload module to a temporary directory
// so that its module context is just those files rather than this repository.
func writeWorkload() (string, error) {
	dir, err := os.MkdirTemp("", "heap-repro-workload-")
	if err != nil {
		return "", err
	}
	err = fs.WalkDir(workloadFS, "testdata/workload", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := workloadFS.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("testdata/workload", path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, rel), data, 0o644)
	})
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

type sampler struct {
	debugAddr string
	start     time.Time
	run       *Run
	phase     atomic.Value
	callsDone atomic.Int64
	lastHeap  atomic.Int64

	mu  sync.Mutex
	err error
}

func (s *sampler) since() float64 {
	return time.Since(s.start).Seconds()
}

func (s *sampler) setPhase(phase string) {
	s.phase.Store(phase)
	s.event(Event{Kind: phase})
}

func (s *sampler) event(ev Event) {
	ev.T = s.since()
	s.mu.Lock()
	s.run.Events = append(s.run.Events, ev)
	s.mu.Unlock()
}

func (s *sampler) loop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := s.sample(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.mu.Lock()
			if s.err == nil {
				s.err = err
			}
			s.mu.Unlock()
			return
		}
	}
}

func (s *sampler) sample(ctx context.Context) error {
	total, sites, err := fetchHeap(ctx, s.debugAddr)
	if err != nil {
		return fmt.Errorf("heap profile: %w", err)
	}
	goroutines, err := fetchGoroutines(ctx, s.debugAddr)
	if err != nil {
		return fmt.Errorf("goroutine count: %w", err)
	}
	runtimes, err := fetchRuntimes(ctx, s.debugAddr)
	if err != nil {
		return fmt.Errorf("lifecycle snapshot: %w", err)
	}
	s.lastHeap.Store(total)
	sample := Sample{
		T:          s.since(),
		Phase:      s.phase.Load().(string),
		CallsDone:  int(s.callsDone.Load()),
		HeapInuse:  total,
		Goroutines: goroutines,
		Runtimes:   runtimes,
		Sites:      sites,
	}
	s.mu.Lock()
	s.run.Samples = append(s.run.Samples, sample)
	s.mu.Unlock()
	return nil
}

func fetchHeap(ctx context.Context, debugAddr string) (int64, map[string]int64, error) {
	// gc=1 collects before profiling, so every sample reflects live objects
	// rather than whatever the last cycle happened to leave behind.
	body, err := get(ctx, debugAddr+"/debug/pprof/heap?gc=1")
	if err != nil {
		return 0, nil, err
	}
	prof, err := profile.Parse(bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	idx := -1
	for i, st := range prof.SampleType {
		if st.Type == "inuse_space" {
			idx = i
		}
	}
	if idx < 0 {
		return 0, nil, errors.New("profile has no inuse_space samples")
	}
	sites := map[string]int64{}
	var total int64
	for _, sample := range prof.Sample {
		v := sample.Value[idx]
		if v == 0 {
			continue
		}
		sites[siteLabel(sample)] += v
		total += v
	}
	return total, sites, nil
}

// siteLabel names the frame that allocated a sample: the innermost frame that
// belongs to a non-standard-library package, so generic helpers such as
// bufio.NewWriterSize or maps.clone are attributed to their caller.
func siteLabel(sample *profile.Sample) string {
	fallback := ""
	for _, loc := range sample.Location {
		for _, line := range loc.Line {
			if line.Function == nil {
				continue
			}
			// Instantiated type arguments can carry package paths of
			// their own, so strip them before classifying the frame.
			name := stripTypeArgs(line.Function.Name)
			if fallback == "" {
				fallback = name
			}
			if isStdlib(name) {
				continue
			}
			return trimSite(name)
		}
	}
	if fallback == "" {
		return "unknown"
	}
	return trimSite(fallback)
}

// stripTypeArgs drops the bracketed type arguments from an instantiated
// generic function's name; they can nest.
func stripTypeArgs(name string) string {
	var b strings.Builder
	depth := 0
	for _, r := range name {
		switch {
		case r == '[':
			depth++
		case r == ']':
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isStdlib reports whether a function belongs to the standard library, whose
// import paths have no dot in their first element.
func isStdlib(name string) bool {
	first := name
	if i := strings.IndexByte(name, '/'); i >= 0 {
		first = name[:i]
	} else if i := strings.IndexByte(name, '.'); i >= 0 {
		first = name[:i]
	}
	return !strings.Contains(first, ".")
}

func trimSite(name string) string {
	for _, prefix := range []string{
		"github.com/dagger/dagger/",
		"go.opentelemetry.io/",
		"google.golang.org/",
		"github.com/",
	} {
		name = strings.TrimPrefix(name, prefix)
	}
	return name
}

func fetchGoroutines(ctx context.Context, debugAddr string) (int, error) {
	body, err := get(ctx, debugAddr+"/debug/pprof/goroutine?debug=1")
	if err != nil {
		return 0, err
	}
	var n int
	if _, err := fmt.Sscanf(string(body), "goroutine profile: total %d", &n); err != nil {
		return 0, fmt.Errorf("parse goroutine header: %w", err)
	}
	return n, nil
}

func fetchRuntimes(ctx context.Context, debugAddr string) (*int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, debugAddr+"/debug/client-lifecycle", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", req.URL, resp.Status)
	}
	var snapshot struct {
		Runtimes int `json:"runtimes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return nil, err
	}
	return &snapshot.Runtimes, nil
}

func get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func humanBytes(n int64) string {
	const mb = 1 << 20
	return fmt.Sprintf("%.1f MB", float64(n)/mb)
}

func chart(args []string) error {
	fl := flag.NewFlagSet("chart", flag.ExitOnError)
	out := fl.String("out", "heap-repro.html", "where to write the chart")
	layers := fl.Int("layers", 12, "how many allocation sites get their own layer; the rest are grouped as other")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() == 0 {
		return errors.New("chart needs at least one recorded run")
	}
	var runs []*Run
	for _, path := range fl.Args() {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var run Run
		if err := json.Unmarshal(data, &run); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if len(run.Samples) == 0 {
			return fmt.Errorf("%s: no samples", path)
		}
		runs = append(runs, &run)
	}
	payload, err := json.Marshal(struct {
		Runs   []*Run `json:"runs"`
		Layers int    `json:"layers"`
	}{runs, *layers})
	if err != nil {
		return err
	}
	// Keep a "</script>" inside a site name from ending the data block.
	safe := strings.ReplaceAll(string(payload), "</", `<\/`)
	page := strings.Replace(chartTemplate, "/*DATA*/null", safe, 1)
	if page == chartTemplate {
		return errors.New("chart template has no data placeholder")
	}
	if err := os.WriteFile(*out, []byte(page), 0o644); err != nil {
		return err
	}
	labels := make([]string, 0, len(runs))
	for _, run := range runs {
		labels = append(labels, run.Label)
	}
	sort.Strings(labels)
	fmt.Fprintf(os.Stderr, "wrote %s (%s)\n", *out, strings.Join(labels, ", "))
	return nil
}
