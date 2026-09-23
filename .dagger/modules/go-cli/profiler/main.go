// go-profiler samples a Go pprof endpoint without depending on the target process.
package main

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxBody = 32 << 20
	maxText = 64 << 10
)

var sampleID = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z$`)
var sampleFiles = map[string]bool{"cpu.pprof": true, "goroutines-before.txt": true, "goroutines-after.txt": true, "metadata.json": true}

type sample struct {
	ID       string            `json:"id"`
	Started  time.Time         `json:"started"`
	Finished time.Time         `json:"finished,omitempty"`
	State    string            `json:"state"`
	CPU      bool              `json:"cpu"`
	Errors   map[string]string `json:"errors,omitempty"`
}

type profiler struct {
	endpoint                  *url.URL
	dir                       string
	seconds, interval, retain int
	// Readers hold the lock while accessing completed samples. Capture does not
	// hold it during network requests; only publication and pruning need it.
	mu      sync.RWMutex
	samples []sample
	current *sample
}

func endpointURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Fragment != "" {
		return nil, fmt.Errorf("expected an absolute HTTP(S) URL")
	}
	return u, nil
}

func newProfiler(endpoint, dir string, seconds, interval, retain int) (*profiler, error) {
	if seconds < 1 || seconds > 60 || interval < 0 || interval > 300 || retain < 1 || retain > 100 {
		return nil, errors.New("seconds must be 1..60, interval 0..300, retain 1..100")
	}
	u, err := endpointURL(endpoint)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	p := &profiler{endpoint: u, dir: dir, seconds: seconds, interval: interval, retain: retain}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !sampleID.MatchString(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name(), "metadata.json"))
		var s sample
		if err != nil || json.Unmarshal(data, &s) != nil || s.ID != entry.Name() || s.Finished.IsZero() {
			// An interrupted capture is not a completed sample.
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				return nil, err
			}
			continue
		}
		p.samples = append(p.samples, s)
	}
	sort.Slice(p.samples, func(i, j int) bool { return p.samples[i].ID < p.samples[j].ID })
	if err := p.prune(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *profiler) prune() error {
	for len(p.samples) > p.retain {
		if err := os.RemoveAll(filepath.Join(p.dir, p.samples[0].ID)); err != nil {
			return err
		}
		p.samples = p.samples[1:]
	}
	return nil
}

func (p *profiler) fetch(ctx context.Context, resource, dest string, query url.Values, timeout time.Duration) error {
	u := *p.endpoint
	u.Path = strings.TrimRight(u.Path, "/") + "/" + resource
	u.RawPath = ""
	q := u.Query()
	for key, values := range query {
		q[key] = values
	}
	u.RawQuery = q.Encode()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("HTTP %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	f, err := os.Create(dest + ".tmp")
	if err != nil {
		return err
	}
	defer os.Remove(dest + ".tmp")
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, maxBody+1))
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n == 0 || n > maxBody {
		return fmt.Errorf("response size %d outside 1..%d bytes", n, maxBody)
	}
	return os.Rename(dest+".tmp", dest)
}

func (p *profiler) capture(ctx context.Context) (sample, error) {
	// Do not accumulate new samples if an earlier retention cleanup failed.
	p.mu.Lock()
	err := p.prune()
	p.mu.Unlock()
	if err != nil {
		return sample{}, err
	}
	now := time.Now().UTC()
	s := sample{ID: now.Format("20060102T150405.000000000Z"), Started: now, State: "capturing", Errors: map[string]string{}}
	dir := filepath.Join(p.dir, s.ID)
	if err := os.Mkdir(dir, 0755); err != nil {
		return s, err
	}
	p.mu.Lock()
	current := s
	current.Errors = nil // Capture mutates its private error map without the lock.
	p.current = &current
	p.mu.Unlock()
	for _, step := range []struct {
		name, resource string
		query          url.Values
		timeout        time.Duration
	}{
		{"goroutines-before.txt", "goroutine", url.Values{"debug": {"2"}}, 10 * time.Second},
		{"cpu.pprof", "profile", url.Values{"seconds": {fmt.Sprint(p.seconds)}}, time.Duration(p.seconds+10) * time.Second},
		{"goroutines-after.txt", "goroutine", url.Values{"debug": {"2"}}, 10 * time.Second},
	} {
		if err := p.fetch(ctx, step.resource, filepath.Join(dir, step.name), step.query, step.timeout); err != nil {
			s.Errors[step.name] = err.Error()
		} else if step.name == "cpu.pprof" {
			s.CPU = true
		}
	}
	s.Finished = time.Now().UTC()
	s.State = "ready"
	if len(s.Errors) > 0 {
		s.State = "partial"
	}
	if !s.CPU {
		s.State = "error"
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0644)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current = nil
	if err != nil {
		os.RemoveAll(dir)
		return s, err
	}
	p.samples = append(p.samples, s)
	if err := p.prune(); err != nil {
		return s, err
	}
	if !s.CPU {
		return s, fmt.Errorf("CPU capture: %s", s.Errors["cpu.pprof"])
	}
	return s, nil
}

func (p *profiler) loop(ctx context.Context) {
	for ctx.Err() == nil {
		s, err := p.capture(ctx)
		log.Printf("sample %s: %s (%s), errors=%v", s.ID, s.State, s.Finished.Sub(s.Started).Round(time.Millisecond), s.Errors)
		if err != nil {
			log.Printf("capture failed: %v", err)
		}
		if s.CPU && ctx.Err() == nil {
			p.mu.RLock()
			text, err := cpuReport(ctx, filepath.Join(p.dir, s.ID, "cpu.pprof"), "cum", "", 12)
			p.mu.RUnlock()
			if err != nil {
				log.Printf("CPU summary: %v", err)
			}
			if text != "" {
				log.Print(text)
			}
		}
		// Even a misconfigured/unavailable endpoint must not become a busy loop.
		delay := time.Duration(p.interval) * time.Second
		if err != nil && delay < time.Second {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Continue consuming output after reaching the limit so subprocess pipes never
// block. exec.CommandContext kills a hung pprof process after a bounded time.
type limitedText struct {
	data      []byte
	truncated bool
}

func (w *limitedText) Write(b []byte) (int, error) {
	n := len(b)
	left := maxText - len(w.data)
	if len(b) > left {
		b = b[:left]
		w.truncated = true
	}
	w.data = append(w.data, b...)
	return n, nil
}
func (w *limitedText) String() string {
	if w.truncated {
		return string(w.data) + "\n[output truncated at 64 KiB]\n"
	}
	return string(w.data)
}

func cpuReport(ctx context.Context, file, view, focus string, count int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"tool", "pprof"}
	if view == "list" {
		args = append(args, "-list="+focus, "-trim_path=/app", "-source_path=/workspace")
	} else {
		args = append(args, "-top", fmt.Sprintf("-nodecount=%d", count))
		if view == "cum" {
			args = append(args, "-cum")
		}
		if focus != "" {
			args = append(args, "-focus="+focus)
		}
	}
	args = append(args, file)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.WaitDelay = time.Second
	out := &limitedText{}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	return out.String(), err
}

func validateView(view, focus string) error {
	goroutines := view == "goroutines-before" || view == "goroutines-after"
	if goroutines && focus != "" {
		return errors.New("focus is only supported for CPU views")
	}
	if !goroutines && view != "cum" && view != "flat" && view != "list" {
		return errors.New("invalid view")
	}
	if len(focus) > 4096 {
		return errors.New("focus is too long")
	}
	if _, err := regexp.Compile(focus); err != nil {
		return errors.New("invalid focus regex")
	}
	if view == "list" && focus == "" {
		return errors.New("list requires focus")
	}
	return nil
}

// Both saved files and retained samples use the same bounded report path.
func fileReport(ctx context.Context, file, view, focus string) (string, error) {
	if err := validateView(view, focus); err != nil {
		return "", err
	}
	info, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > maxBody {
		return "", errors.New("profile must be a regular file of 1..32 MiB")
	}
	if view != "goroutines-before" && view != "goroutines-after" {
		return cpuReport(ctx, file, view, focus, 35)
	}
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	out := &limitedText{}
	if _, err := io.Copy(out, io.LimitReader(f, maxText+1)); err != nil {
		return "", err
	}
	if !strings.HasPrefix(string(out.data), "goroutine ") {
		return "", errors.New("expected a debug=2 goroutine text dump")
	}
	return out.String(), nil
}

func localReport(ctx context.Context, dir, file, view, focus string) (string, error) {
	if !filepath.IsLocal(file) || strings.Contains(file, "\\") || slices.Contains(strings.Split(file, "/"), "..") || slices.Contains(strings.Split(file, "/"), ".") {
		return "", errors.New("profile path must be workspace-relative, without '.', '..', or backslashes")
	}
	if err := validateView(view, focus); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := root.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > maxBody {
		return "", errors.New("profile must be a regular file of 1..32 MiB")
	}
	// Copy through the confined root: pprof must not follow a workspace symlink
	// outside it, interpret a filename as a URL/flag, or modify the source file.
	tmp, err := os.CreateTemp("", "go-profiler-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	n, copyErr := io.Copy(tmp, io.LimitReader(f, maxBody+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n > maxBody {
		return "", errors.New("profile exceeds 32 MiB")
	}
	return fileReport(ctx, tmp.Name(), view, focus)
}

func (p *profiler) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok\n") })
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		p.mu.RLock()
		defer p.mu.RUnlock()
		// Keep the newest captures visible even when tool output is truncated.
		// Retention still uses the original oldest-first slice.
		samples := slices.Clone(p.samples)
		slices.Reverse(samples)
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		encoder.Encode(struct {
			Endpoint string   `json:"endpoint"`
			Seconds  int      `json:"seconds"`
			Interval int      `json:"interval"`
			Retain   int      `json:"retain"`
			Current  *sample  `json:"current"`
			Samples  []sample `json:"samples"`
		}{p.endpoint.String(), p.seconds, p.interval, p.retain, p.current, samples})
	})
	mux.HandleFunc("/report", p.report)
	mux.HandleFunc("/archive", p.archive)
	// File-list and GO111MODULE=off builds may select the pre-Go 1.22 mux.
	// Use path-only patterns and enforce methods independently of that default.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (p *profiler) report(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid query", 400)
		return
	}
	for key, values := range q {
		if (key != "sample" && key != "view" && key != "focus") || len(values) != 1 {
			http.Error(w, "unknown or repeated query parameter", 400)
			return
		}
	}
	id, view, focus := q.Get("sample"), q.Get("view"), q.Get("focus")
	if id == "" {
		id = "latest"
	}
	if view == "" {
		view = "cum"
	}
	if id != "latest" && !sampleID.MatchString(id) {
		http.Error(w, "invalid sample ID", 400)
		return
	}
	if err := validateView(view, focus); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	goroutines := view == "goroutines-before" || view == "goroutines-after"
	p.mu.RLock()
	defer p.mu.RUnlock()
	var selected *sample
	for i := len(p.samples) - 1; i >= 0; i-- {
		s := &p.samples[i]
		if (id == "latest" && (goroutines || s.CPU)) || s.ID == id {
			selected = s
			break
		}
	}
	if selected == nil {
		http.Error(w, "no matching completed sample", 404)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	name := "cpu.pprof"
	if goroutines {
		name = view + ".txt"
	} else if !selected.CPU {
		http.Error(w, "CPU profile unavailable: "+selected.Errors[name], 404)
		return
	}
	text, err := fileReport(r.Context(), filepath.Join(p.dir, selected.ID, name), view, focus)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, fmt.Sprintf("report: %v\n%s\n%s", err, text, selected.Errors[name]), status)
		return
	}
	io.WriteString(w, text)
}

func (p *profiler) archive(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	w.Header().Set("Content-Type", "application/x-tar")
	tw := tar.NewWriter(w)
	defer tw.Close()
	for _, s := range p.samples {
		for _, name := range []string{"metadata.json", "cpu.pprof", "goroutines-before.txt", "goroutines-after.txt"} {
			f, err := os.Open(filepath.Join(p.dir, s.ID, name))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				log.Printf("archive: %v", err)
				return
			}
			info, err := f.Stat()
			if err == nil {
				err = tw.WriteHeader(&tar.Header{Name: s.ID + "/" + name, Mode: 0644, Size: info.Size(), ModTime: info.ModTime()})
			}
			if err == nil {
				_, err = io.Copy(tw, f)
			}
			f.Close()
			if err != nil {
				log.Printf("archive: %v", err)
				return
			}
		}
	}
}

func request(ctx context.Context, base, path string) (*http.Response, error) {
	u, err := endpointURL(base)
	if err != nil {
		return nil, err
	}
	ref, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || ref.IsAbs() || ref.Host != "" || ref.Fragment != "" {
		return nil, errors.New("path must be a relative /path with optional query")
	}
	u.Path, u.RawPath, u.RawQuery = ref.Path, ref.RawPath, ref.RawQuery
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxText+1024))
		return nil, fmt.Errorf("HTTP %s: %s", resp.Status, body)
	}
	return resp, nil
}

func extract(reader io.Reader, dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(reader)
	seen := map[string]bool{}
	directories := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		parts := strings.Split(strings.TrimSuffix(h.Name, "/"), "/")
		if len(parts) < 1 || !sampleID.MatchString(parts[0]) || strings.Contains(h.Name, "\\") {
			return fmt.Errorf("unsafe archive path %q", h.Name)
		}
		directories[parts[0]] = true
		if len(directories) > 100 {
			return errors.New("archive exceeds 100 samples")
		}
		if h.Typeflag == tar.TypeDir && len(parts) == 1 {
			if err := root.MkdirAll(parts[0], 0755); err != nil {
				return err
			}
			continue
		}
		if h.Typeflag != tar.TypeReg || len(parts) != 2 || !sampleFiles[parts[1]] || h.Size < 0 || h.Size > maxBody || seen[h.Name] || len(seen) >= 400 {
			return fmt.Errorf("invalid archive entry %q", h.Name)
		}
		seen[h.Name] = true
		if err := root.MkdirAll(parts[0], 0755); err != nil {
			return err
		}
		if info, err := root.Lstat(h.Name); err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular destination %q", h.Name)
		}
		f, err := root.OpenFile(h.Name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, tr)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: go-profiler serve|capture|client|report|export [flags]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	switch args[0] {
	case "serve", "capture":
		endpoint := flags.String("endpoint", "", "pprof base URL, e.g. http://engine:6060/debug/pprof")
		seconds := flags.Int("seconds", 15, "CPU capture duration (1..60 seconds)")
		interval := flags.Int("interval", 5, "pause between captures (0..300 seconds)")
		retain := flags.Int("retain", 20, "completed samples to retain (1..100)")
		listen := flags.String("listen", ":8080", "HTTP listen address")
		dir := flags.String("dir", "/profiles", "sample directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected positional arguments")
		}
		p, err := newProfiler(*endpoint, *dir, *seconds, *interval, *retain)
		if err != nil {
			return err
		}
		if args[0] == "capture" {
			s, err := p.capture(ctx)
			if err != nil || len(s.Errors) > 0 {
				return fmt.Errorf("capture %s (%s): %v; diagnostics=%v; ensure no other CPU profiler is running on the target", s.ID, s.State, err, s.Errors)
			}
			fmt.Print(s.ID)
			return nil
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := make(chan struct{})
		go func() { defer close(done); p.loop(ctx) }()
		server := &http.Server{Addr: *listen, Handler: p.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10,
			BaseContext: func(net.Listener) context.Context { return ctx },
		}
		shutdownDone := make(chan struct{})
		go func() {
			defer close(shutdownDone)
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if server.Shutdown(shutdown) != nil {
				server.Close()
			}
		}()
		err = server.ListenAndServe()
		cancel()
		<-done
		<-shutdownDone
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case "client", "report", "export":
		base := flags.String("url", "http://profiler:8080", "profiler HTTP URL")
		dir := flags.String("dir", "/out", "archive destination")
		id := flags.String("sample", "latest", "sample ID or latest")
		view := flags.String("view", "cum", "cum, flat, list, goroutines-before, goroutines-after")
		focus := flags.String("focus", "", "pprof focus regex (required for list)")
		file := flags.String("file", "", "saved workspace-relative profile; report only")
		root := flags.String("root", ".", "workspace root for saved-file reports")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		fileSet := false
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "file" {
				fileSet = true
			}
		})
		if fileSet {
			if args[0] != "report" || flags.NArg() != 0 || *id != "latest" {
				return errors.New("file requires report and cannot be combined with a sample ID or positional arguments")
			}
			text, err := localReport(ctx, *root, *file, *view, *focus)
			fmt.Print(text)
			return err
		}
		path := "/archive"
		if args[0] == "client" {
			if flags.NArg() != 1 {
				return errors.New("client requires one /path argument")
			}
			path = flags.Arg(0)
		} else if flags.NArg() != 0 {
			return errors.New("unexpected positional arguments")
		}
		if args[0] == "report" {
			path = "/report?" + url.Values{"sample": {*id}, "view": {*view}, "focus": {*focus}}.Encode()
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		resp, err := request(ctx, *base, path)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if args[0] == "export" {
			return extract(resp.Body, *dir)
		}
		n, err := io.Copy(os.Stdout, io.LimitReader(resp.Body, maxBody+1))
		if err == nil && n > maxBody {
			return errors.New("response exceeds 32 MiB")
		}
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
