// Package fixturetransport is the in-process HTTP transport of the
// environment-gated remote-cache test fixture. It is test-only by convention
// and nil off-gate: until the engine enables it at startup, Wrap returns its
// argument unchanged and nothing else in this package does anything.
//
// Enabled, it answers requests to the exact fixture hosts from a copied,
// immutable response script and real files under the fixture root, and
// delegates every other request to the transport it wraps, so ordinary
// registry, SDK and DNS-aware traffic is untouched. An unknown URL on a
// fixture host fails explicitly; it never falls through to DNS or the
// network. No listener, HTTP server or remote-cache service is involved.
package fixturetransport

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// The exact hosts the dispatcher answers.
const (
	OriginHost  = "origin.remote-cache.invalid"
	ContentHost = "content.remote-cache.invalid"
	GitHost     = "git.remote-cache.invalid"
)

// ErrUnscripted is returned for a fixture-host URL the script does not name.
var ErrUnscripted = errors.New("fixture transport: no scripted response")

// ErrFault is the named transport fault a response can inject before headers.
var ErrFault = errors.New("fixture transport fault")

// Response is one scripted reply. It sets a status, headers and a body file,
// or a named read fault; it is never a cache output.
type Response struct {
	Method string `json:"method,omitempty"` // empty matches any method
	URL    string `json:"url"`              // exact URL, without fragment
	Status int    `json:"status,omitempty"` // zero means 200
	// Headers are returned as given.
	Headers map[string]string `json:"headers,omitempty"`
	// BodyFile is a contained path under the fixture root. Empty is no body.
	BodyFile string `json:"bodyFile,omitempty"`
	// Rangeable makes a Range request answer 206 with the requested bytes.
	Rangeable bool `json:"rangeable,omitempty"`
	// Fault is "" or one of: "transport" (no response at all) and "truncate"
	// (the body ends early with io.ErrUnexpectedEOF after TruncateAt bytes).
	Fault      string `json:"fault,omitempty"`
	TruncateAt int64  `json:"truncateAt,omitempty"`
}

// Script is one immutable generation of responses.
type Script struct {
	Responses []Response `json:"responses"`
}

// Observation is what the dispatcher saw of one fixture-host request.
type Observation struct {
	Method          string `json:"method"`
	URL             string `json:"url"`
	IfNoneMatch     string `json:"ifNoneMatch,omitempty"`
	IfModifiedSince string `json:"ifModifiedSince,omitempty"`
	Authorization   bool   `json:"authorization"`
	Range           string `json:"range,omitempty"`
	Status          int    `json:"status,omitempty"`
	Error           string `json:"error,omitempty"`
	BodyBytesRead   int64  `json:"bodyBytesRead"`
	Closed          bool   `json:"closed"`
}

// Report is the dispatcher's bounded observation list.
type Report struct {
	Generation   uint64         `json:"generation"`
	Requests     []*Observation `json:"requests"`
	Delegated    uint64         `json:"delegated"`
	FixtureHosts uint64         `json:"fixtureHostRequests"`
	Overflowed   bool           `json:"overflowed"`
}

type generation struct {
	number    uint64
	responses map[string]Response
}

// Dispatcher is the enabled engine's one dispatcher.
type Dispatcher struct {
	root string
	gen  atomic.Pointer[generation]

	mu         sync.Mutex
	cap        int
	requests   []*Observation
	overflowed bool
	delegated  atomic.Uint64
	fixture    atomic.Uint64
}

var current atomic.Pointer[Dispatcher]

// DefaultObservationCap bounds the observation list. Overflow is reported and
// fails a test; observations are never silently dropped.
const DefaultObservationCap = 4096

// Enable installs the process's dispatcher. The engine calls it once at
// startup, under the fixture gate, before any request. Calling it again
// returns the existing dispatcher.
func Enable(root string) (*Dispatcher, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("fixture transport root must be absolute")
	}
	d := &Dispatcher{root: root, cap: DefaultObservationCap}
	d.gen.Store(&generation{responses: map[string]Response{}})
	if current.CompareAndSwap(nil, d) {
		return d, nil
	}
	return current.Load(), nil
}

// Current returns the dispatcher, or nil off-gate.
func Current() *Dispatcher { return current.Load() }

// Wrap returns base unchanged off-gate. Enabled, it returns a transport that
// answers the fixture hosts and delegates everything else to base.
func Wrap(base http.RoundTripper) http.RoundTripper {
	d := current.Load()
	if d == nil {
		return base
	}
	return &roundTripper{dispatcher: d, base: base}
}

func scriptKey(method, url string) string { return method + " " + url }

// SetScript swaps in a copied script as a new immutable generation.
func (d *Dispatcher) SetScript(script Script) (uint64, error) {
	next := &generation{responses: make(map[string]Response, len(script.Responses))}
	for _, response := range script.Responses {
		if response.URL == "" {
			return 0, fmt.Errorf("fixture transport: a response needs a URL")
		}
		switch response.Fault {
		case "", "transport", "truncate":
		default:
			return 0, fmt.Errorf("fixture transport: unknown fault %q", response.Fault)
		}
		if response.BodyFile != "" {
			if filepath.IsAbs(response.BodyFile) || !filepath.IsLocal(response.BodyFile) {
				return 0, fmt.Errorf("fixture transport: body file %q is not contained", response.BodyFile)
			}
		}
		next.responses[scriptKey(response.Method, response.URL)] = response
	}
	for {
		old := d.gen.Load()
		next.number = old.number + 1
		if d.gen.CompareAndSwap(old, next) {
			return next.number, nil
		}
	}
}

// SetObservationCap sets the bound and clears the list.
func (d *Dispatcher) SetObservationCap(n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cap, d.requests, d.overflowed = n, nil, false
}

// Report copies the current observations.
func (d *Dispatcher) Report() Report {
	d.mu.Lock()
	defer d.mu.Unlock()
	report := Report{Generation: d.gen.Load().number, Delegated: d.delegated.Load(), FixtureHosts: d.fixture.Load(), Overflowed: d.overflowed}
	for _, request := range d.requests {
		copied := *request
		report.Requests = append(report.Requests, &copied)
	}
	return report
}

func (d *Dispatcher) observe(req *http.Request) *Observation {
	observation := &Observation{
		Method:          req.Method,
		URL:             requestURL(req),
		IfNoneMatch:     req.Header.Get("If-None-Match"),
		IfModifiedSince: req.Header.Get("If-Modified-Since"),
		Authorization:   req.Header.Get("Authorization") != "",
		Range:           req.Header.Get("Range"),
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.requests) >= d.cap {
		d.overflowed = true
		return observation
	}
	d.requests = append(d.requests, observation)
	return observation
}

func (d *Dispatcher) update(observation *Observation, fn func(*Observation)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fn(observation)
}

func requestURL(req *http.Request) string {
	u := *req.URL
	u.Fragment = ""
	return u.String()
}

func isFixtureHost(host string) bool {
	switch strings.ToLower(host) {
	case OriginHost, ContentHost, GitHost:
		return true
	}
	return false
}

type roundTripper struct {
	dispatcher *Dispatcher
	base       http.RoundTripper
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	d := rt.dispatcher
	if !isFixtureHost(req.URL.Hostname()) {
		d.delegated.Add(1)
		if rt.base == nil {
			return nil, fmt.Errorf("fixture transport: no delegate for %s", req.URL.Host)
		}
		return rt.base.RoundTrip(req)
	}
	d.fixture.Add(1)
	observation := d.observe(req)
	fail := func(err error) (*http.Response, error) {
		d.update(observation, func(o *Observation) { o.Error = err.Error() })
		return nil, err
	}
	gen := d.gen.Load()
	response, ok := gen.responses[scriptKey(req.Method, observation.URL)]
	if !ok {
		response, ok = gen.responses[scriptKey("", observation.URL)]
	}
	if !ok {
		return fail(fmt.Errorf("%w for %s %s", ErrUnscripted, req.Method, observation.URL))
	}
	if response.Fault == "transport" {
		return fail(fmt.Errorf("%w: %s", ErrFault, observation.URL))
	}
	return d.respond(req, response, observation)
}

func (d *Dispatcher) respond(req *http.Request, response Response, observation *Observation) (*http.Response, error) {
	status := response.Status
	if status == 0 {
		status = http.StatusOK
	}
	header := http.Header{}
	for key, value := range response.Headers {
		header.Set(key, value)
	}
	var body io.ReadCloser = http.NoBody
	var length int64
	if response.BodyFile != "" && req.Method != http.MethodHead {
		root, err := os.OpenRoot(d.root)
		if err != nil {
			return nil, err
		}
		file, err := root.Open(response.BodyFile)
		root.Close()
		if err != nil {
			d.update(observation, func(o *Observation) { o.Error = err.Error() })
			return nil, fmt.Errorf("fixture transport: body file: %w", err)
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, err
		}
		offset, size := int64(0), info.Size()
		if response.Rangeable && status == http.StatusOK {
			if start, end, ok := parseRange(req.Header.Get("Range"), size); ok {
				offset, size = start, end-start+1
				status = http.StatusPartialContent
				header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, info.Size()))
			}
		}
		length = size
		reader := io.Reader(io.NewSectionReader(file, offset, size))
		truncate := int64(-1)
		if response.Fault == "truncate" {
			truncate = response.TruncateAt
		}
		body = &observedBody{reader: reader, closer: file, dispatcher: d, observation: observation, truncateAt: truncate}
	}
	if header.Get("Content-Length") == "" {
		header.Set("Content-Length", strconv.FormatInt(length, 10))
	}
	d.update(observation, func(o *Observation) { o.Status = status })
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          body,
		ContentLength: length,
		Request:       req,
	}, nil
}

// parseRange handles the single "bytes=a-b" and "bytes=a-" forms a chain
// reader sends.
func parseRange(value string, size int64) (start, end int64, ok bool) {
	spec, found := strings.CutPrefix(value, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false
	}
	first, last, found := strings.Cut(spec, "-")
	if !found || first == "" {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end = size - 1
	if last != "" {
		if end, err = strconv.ParseInt(last, 10, 64); err != nil || end < start {
			return 0, 0, false
		}
		end = min(end, size-1)
	}
	return start, end, true
}

// observedBody streams the actual file and counts what the client really
// read. A truncated body ends with io.ErrUnexpectedEOF after truncateAt bytes;
// the earlier bytes and the Close obligation stay real.
type observedBody struct {
	reader      io.Reader
	closer      io.Closer
	dispatcher  *Dispatcher
	observation *Observation
	truncateAt  int64
	read        int64
}

func (b *observedBody) Read(p []byte) (int, error) {
	if b.truncateAt >= 0 {
		remaining := b.truncateAt - b.read
		if remaining <= 0 {
			return 0, io.ErrUnexpectedEOF
		}
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
	}
	n, err := b.reader.Read(p)
	b.read += int64(n)
	b.dispatcher.update(b.observation, func(o *Observation) { o.BodyBytesRead = b.read })
	return n, err
}

func (b *observedBody) Close() error {
	b.dispatcher.update(b.observation, func(o *Observation) { o.Closed = true })
	return b.closer.Close()
}
