package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T, handler http.HandlerFunc, retain int) (*profiler, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	p, err := newProfiler(server.URL+"/debug/pprof", t.TempDir(), 1, 0, retain)
	if err != nil {
		t.Fatal(err)
	}
	return p, server
}

func successfulEndpoint(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/profile") {
		io.WriteString(w, "profile bytes")
		return
	}
	io.WriteString(w, "goroutine 1 [running]:\nmain.main()\n")
}

func TestCaptureRetentionAndArchive(t *testing.T) {
	var calls []string
	p, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.RequestURI())
		successfulEndpoint(w, r)
	}, 2)
	var samples []sample
	for range 3 {
		s, err := p.capture(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if s.State != "ready" || !s.CPU || s.Finished.Before(s.Started) {
			t.Fatalf("bad sample: %+v", s)
		}
		samples = append(samples, s)
	}
	if got := strings.Join(calls[:3], ","); got != "/debug/pprof/goroutine?debug=2,/debug/pprof/profile?seconds=1,/debug/pprof/goroutine?debug=2" {
		t.Fatal(got)
	}
	if len(p.samples) != 2 {
		t.Fatalf("retained %d samples", len(p.samples))
	}
	if _, err := os.Stat(filepath.Join(p.dir, samples[0].ID)); !os.IsNotExist(err) {
		t.Fatalf("old sample still exists: %v", err)
	}
	p2, err := newProfiler(p.endpoint.String(), p.dir, 1, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.samples) != 1 || p2.samples[0].ID != samples[2].ID {
		t.Fatalf("restart retention: %+v", p2.samples)
	}
	rec := httptest.NewRecorder()
	p2.handler().ServeHTTP(rec, httptest.NewRequest("GET", "/archive", nil))
	out := t.TempDir()
	if err := extract(rec.Body, out); err != nil {
		t.Fatal(err)
	}
	for name := range sampleFiles {
		if _, err := os.Stat(filepath.Join(out, samples[2].ID, name)); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(out, samples[2].ID, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s sample
	if err := json.Unmarshal(data, &s); err != nil || s.State != "ready" {
		t.Fatalf("metadata: %s, %v", data, err)
	}
}

func TestCaptureErrorsAndRetry(t *testing.T) {
	for _, cpuFails := range []bool{false, true} {
		t.Run(fmt.Sprint(cpuFails), func(t *testing.T) {
			fail := true
			p, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if fail && strings.HasSuffix(r.URL.Path, "/profile") == cpuFails {
					http.Error(w, "unavailable", 503)
					return
				}
				successfulEndpoint(w, r)
			}, 2)
			s, err := p.capture(t.Context())
			if cpuFails {
				if err == nil || s.State != "error" || s.CPU {
					t.Fatalf("sample=%+v err=%v", s, err)
				}
			} else if err != nil || s.State != "partial" || !s.CPU {
				t.Fatalf("sample=%+v err=%v", s, err)
			}
			if len(s.Errors) == 0 {
				t.Fatal("missing diagnostics")
			}
			fail = false
			s, err = p.capture(t.Context())
			if err != nil || s.State != "ready" {
				t.Fatalf("retry: %+v %v", s, err)
			}
		})
	}
}

func TestHealthAndStatusDuringCapture(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	p, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/profile") {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		successfulEndpoint(w, r)
	}, 2)
	defer close(release)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := p.capture(ctx); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("capture did not start")
	}
	for _, path := range []string{"/health", "/status", "/archive"} {
		rec := httptest.NewRecorder()
		p.handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		if path == "/status" && !strings.Contains(rec.Body.String(), `"state": "capturing"`) {
			t.Fatal(rec.Body.String())
		}
		if path == "/archive" {
			if h, err := tar.NewReader(rec.Body).Next(); err != io.EOF {
				t.Fatalf("unfinished sample archived: %v %v", h, err)
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancellation error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capture ignored cancellation")
	}
}

func TestStatusNewestFirst(t *testing.T) {
	p, _ := fixture(t, successfulEndpoint, 3)
	var captured []sample
	for range 3 {
		s, err := p.capture(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		captured = append(captured, s)
	}
	rec := httptest.NewRecorder()
	p.handler().ServeHTTP(rec, httptest.NewRequest("GET", "/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d: %s", rec.Code, rec.Body.String())
	}
	var status struct {
		Samples []sample `json:"samples"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Samples) != len(captured) {
		t.Fatalf("got %d samples, want %d", len(status.Samples), len(captured))
	}
	for i, s := range status.Samples {
		if s.ID != captured[len(captured)-1-i].ID {
			t.Fatalf("sample %d is not newest-first: %+v", i, status.Samples)
		}
		if p.samples[i].ID != captured[i].ID {
			t.Fatal("status changed internal retention order")
		}
	}
	// A status read must not change which sample the next capture evicts.
	if _, err := p.capture(t.Context()); err != nil {
		t.Fatal(err)
	}
	if p.samples[0].ID != captured[1].ID {
		t.Fatal("capture evicted the wrong sample after status")
	}
}

func TestFetchBounded(t *testing.T) {
	p, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/debug/pprof/large" {
			chunk := make([]byte, 1<<20)
			for range 33 {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
			return
		}
		<-r.Context().Done()
	}, 1)
	for _, resource := range []string{"large", "hung"} {
		dest := filepath.Join(t.TempDir(), "result")
		err := p.fetch(t.Context(), resource, dest, nil, 100*time.Millisecond)
		if err == nil {
			t.Fatal("expected bounded fetch to fail")
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatalf("failed capture kept output: %v", err)
		}
		if _, err := os.Stat(dest + ".tmp"); !os.IsNotExist(err) {
			t.Fatalf("failed capture kept temporary output: %v", err)
		}
	}
}

func TestReportValidation(t *testing.T) {
	p, _ := fixture(t, successfulEndpoint, 2)
	if _, err := p.capture(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"view=invalid", "view=list", "focus=%5B", "sample=../etc", "sample=a&sample=b", "view=cum&view=flat", "unknown=true", "focus=%zz", "view=goroutines-before&focus=foo",
	} {
		rec := httptest.NewRecorder()
		p.handler().ServeHTTP(rec, httptest.NewRequest("GET", "/report?"+query, nil))
		if rec.Code != 400 {
			t.Fatalf("%q: got %d: %s", query, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	p.handler().ServeHTTP(rec, httptest.NewRequest("GET", "/report?view=goroutines-after", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "goroutine 1") {
		t.Fatalf("%d: %s", rec.Code, rec.Body.String())
	}
}

func archiveEntry(t *testing.T, name string, kind byte, link string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	h := &tar.Header{Name: name, Typeflag: kind, Mode: 0644, Linkname: link}
	if kind == tar.TypeReg {
		h.Size = 4
	}
	if err := tw.WriteHeader(h); err != nil {
		t.Fatal(err)
	}
	if kind == tar.TypeReg {
		tw.Write([]byte("data"))
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestExportRejectsUnsafeArchives(t *testing.T) {
	id := "20260101T010101.123456789Z"
	for _, name := range []string{"../outside", "/tmp/outside", id + "/../../outside", id + "/unknown", id + "\\cpu.pprof", "cpu.pprof"} {
		if err := extract(archiveEntry(t, name, tar.TypeReg, ""), t.TempDir()); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink} {
		if err := extract(archiveEntry(t, id+"/cpu.pprof", kind, "../../outside"), t.TempDir()); err == nil {
			t.Fatal("accepted link")
		}
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, id)); err != nil {
		t.Fatal(err)
	}
	if err := extract(archiveEntry(t, id+"/cpu.pprof", tar.TypeReg, ""), root); err == nil {
		t.Fatal("followed pre-existing directory symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "cpu.pprof")); !os.IsNotExist(err) {
		t.Fatal("wrote outside extraction directory")
	}
}

func TestClientQueryEncoding(t *testing.T) {
	focus := `foo\.(bar|baz)\[x\] &value=#literal`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/report" || r.URL.Query().Get("focus") != focus {
			t.Errorf("bad request: %s", r.URL)
		}
	}))
	defer server.Close()
	if err := run(t.Context(), []string{"report", "--url", server.URL, "--view", "list", "--focus", focus}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"http://example.com", "//example.com/a", "status"} {
		if _, err := request(t.Context(), server.URL, path); err == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
	failure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "not ready", 503) }))
	defer failure.Close()
	if _, err := request(t.Context(), failure.URL, "/status"); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("HTTP failure: %v", err)
	}
}

func cpuFixture(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "cpu.pprof")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		for i := 0; i < 10000; i++ {
			_ = fmt.Sprint(i)
		}
	}
	pprof.StopCPUProfile()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestCPUReport(t *testing.T) {
	file := cpuFixture(t)
	for _, view := range []string{"cum", "flat"} {
		text, err := cpuReport(t.Context(), file, view, "", 35)
		if err != nil {
			t.Fatalf("%s: %v\n%s", view, err, text)
		}
		if !strings.Contains(text, "flat") || !strings.Contains(text, "cum") {
			t.Fatal(text)
		}
	}
	p, _ := fixture(t, successfulEndpoint, 3)
	s, err := p.capture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.dir, s.ID, "cpu.pprof"), data, 0644); err != nil {
		t.Fatal(err)
	}
	// A more recent failed capture must not shadow the latest successful CPU.
	p.endpoint, _ = url.Parse("http://127.0.0.1:1/debug/pprof")
	if _, err := p.capture(t.Context()); err == nil {
		t.Fatal("expected capture error")
	}
	rec := httptest.NewRecorder()
	p.handler().ServeHTTP(rec, httptest.NewRequest("GET", "/report?sample=latest&view=cum", nil))
	if rec.Code != 200 {
		t.Fatalf("latest successful: %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCaptureAndSavedReports(t *testing.T) {
	data, err := os.ReadFile(cpuFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/profile") {
			w.Write(data)
		} else {
			successfulEndpoint(w, r)
		}
	}))
	defer endpoint.Close()
	dir := t.TempDir()
	if err := run(t.Context(), []string{"capture", "--endpoint", endpoint.URL + "/debug/pprof", "--seconds", "1", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !sampleID.MatchString(entries[0].Name()) {
		t.Fatalf("capture directory: %v, %v", entries, err)
	}
	id := entries[0].Name()
	endpoint.Close() // Saved reports need neither the target nor a profiler service.
	for _, view := range []string{"cum", "flat", "list", "goroutines-before", "goroutines-after"} {
		name, focus := "cpu.pprof", ""
		if strings.HasPrefix(view, "goroutines-") {
			name = view + ".txt"
		}
		if view == "list" {
			focus = "cpuFixture"
		}
		if err := run(t.Context(), []string{"report", "--root", dir, "--file", id + "/" + name, "--view", view, "--focus", focus, "--url", "http://127.0.0.1:1"}); err != nil {
			t.Fatalf("saved %s: %v", view, err)
		}
	}
	// Exercise a monitor export, shut down its HTTP service, then use only files.
	p, err := newProfiler(endpoint.URL, dir, 1, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	service := httptest.NewServer(p.handler())
	exported := t.TempDir()
	if err := run(t.Context(), []string{"export", "--url", service.URL, "--dir", exported}); err != nil {
		service.Close()
		t.Fatal(err)
	}
	service.Close()
	for _, name := range []string{"cpu.pprof", "goroutines-after.txt"} {
		view := "cum"
		if name != "cpu.pprof" {
			view = "goroutines-after"
		}
		text, err := localReport(t.Context(), exported, id+"/"+name, view, "")
		if err != nil || text == "" {
			t.Fatalf("exported %s: %v\n%s", name, err, text)
		}
	}
	original, err := os.ReadFile(filepath.Join(dir, id, "cpu.pprof"))
	if err != nil || !bytes.Equal(original, data) {
		t.Fatal("analysis modified the source profile")
	}
}

func TestCaptureCommandFailures(t *testing.T) {
	for _, cpuFails := range []bool{false, true} {
		t.Run(fmt.Sprint(cpuFails), func(t *testing.T) {
			_, endpoint := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/profile") == cpuFails {
					http.Error(w, "profiling already in use", 500)
					return
				}
				successfulEndpoint(w, r)
			}, 1)
			dir := t.TempDir()
			err := run(t.Context(), []string{"capture", "--endpoint", endpoint.URL, "--seconds", "1", "--dir", dir})
			if err == nil || !strings.Contains(err.Error(), "profiling already in use") || !strings.Contains(err.Error(), "diagnostics=") {
				t.Fatalf("expected capture diagnostics, got %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("capture directory: %v %v", entries, err)
			}
			if _, err := os.Stat(filepath.Join(dir, entries[0].Name(), "metadata.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSavedReportValidation(t *testing.T) {
	dir := t.TempDir()
	for name, contents := range map[string]string{"invalid.pprof": "not a profile", "empty.pprof": "", "dump.txt": "goroutine 1 [running]:\nmain.main()\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(cpuFixture(t), filepath.Join(dir, "outside.pprof")); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"", "../outside.pprof", "a/../dump.txt", "/etc/passwd", "a\\dump.txt", "./dump.txt", "outside.pprof", "missing.pprof", "invalid.pprof", "empty.pprof", "."} {
		if _, err := localReport(t.Context(), dir, file, "cum", ""); err == nil {
			t.Fatalf("accepted invalid profile %q", file)
		}
	}
	for _, options := range [][2]string{{"invalid", ""}, {"list", ""}, {"cum", "["}, {"goroutines-after", "focus"}, {"cum", strings.Repeat("a", 4097)}} {
		if _, err := localReport(t.Context(), dir, "dump.txt", options[0], options[1]); err == nil {
			t.Fatalf("accepted report options %v", options)
		}
	}
	if _, err := localReport(t.Context(), dir, "invalid.pprof", "goroutines-after", ""); err == nil {
		t.Fatal("accepted non-goroutine text")
	}
	for _, args := range [][]string{
		{"report", "--file", "dump.txt", "--sample", "20260101T010101.123456789Z"},
		{"report", "--file", "dump.txt", "unexpected"},
		{"report", "--file", ""},
		{"export", "--file", "dump.txt"},
	} {
		if err := run(t.Context(), args); err == nil {
			t.Fatalf("accepted arguments %v", args)
		}
	}
	p, _ := fixture(t, successfulEndpoint, 1)
	rec := httptest.NewRecorder()
	p.handler().ServeHTTP(rec, httptest.NewRequest("GET", "/report?file=dump.txt", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatal("HTTP reporting accepted local file selection")
	}
}

func TestTextLimit(t *testing.T) {
	w := &limitedText{}
	data := bytes.Repeat([]byte("x"), maxText+100)
	if n, err := w.Write(data); err != nil || n != len(data) {
		t.Fatalf("write: %d %v", n, err)
	}
	if len(w.data) != maxText || !strings.Contains(w.String(), "[output truncated") {
		t.Fatal("missing truncation")
	}
}

func TestStandaloneRoutes(t *testing.T) {
	// Standalone builds do not inherit the repository's go directive, which
	// changes net/http's default routing semantics. Exercise that build mode.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-race", "-run", "^Test(HealthAndStatusDuringCapture|ReportValidation|CaptureRetentionAndArchive|Methods)$", ".")
	cmd.Env = append(os.Environ(), "GO111MODULE=off", "GOWORK=off", "GODEBUG=httpmuxgo121=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("standalone tests: %v\n%s", err, output)
	}
}

func TestMethods(t *testing.T) {
	p, _ := fixture(t, successfulEndpoint, 1)
	for _, path := range []string{"/health", "/status", "/report", "/archive"} {
		rec := httptest.NewRecorder()
		p.handler().ServeHTTP(rec, httptest.NewRequest("POST", path, nil))
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("%s: status=%d allow=%q", path, rec.Code, rec.Header().Get("Allow"))
		}
	}
}

func TestBounds(t *testing.T) {
	for _, bounds := range [][3]int{{0, 5, 20}, {61, 5, 20}, {15, -1, 20}, {15, 301, 20}, {15, 5, 0}, {15, 5, 101}} {
		if _, err := newProfiler("http://localhost/debug/pprof", t.TempDir(), bounds[0], bounds[1], bounds[2]); err == nil {
			t.Fatalf("accepted %v", bounds)
		}
	}
}
