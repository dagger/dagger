package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

type TestServer struct {
	*httptest.Server
	mu     sync.Mutex
	routes map[string]Response
	stats  map[string]*Stat
}

func NewTestServer(routes map[string]Response) *TestServer {
	ts := &TestServer{
		routes: routes,
		stats:  map[string]*Stat{},
	}
	ts.Server = httptest.NewServer(ts)
	return ts
}

func (s *TestServer) SetRoute(name string, resp Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[name] = resp
}

func (s *TestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	resp, ok := s.routes[r.URL.Path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		s.mu.Unlock()
		return
	}

	if _, ok := s.stats[r.URL.Path]; !ok {
		s.stats[r.URL.Path] = &Stat{}
	}

	s.stats[r.URL.Path].AllRequests++
	s.stats[r.URL.Path].Requests = append(s.stats[r.URL.Path].Requests, newRequest(r))

	if resp.LastModified != nil {
		w.Header().Set("Last-Modified", resp.LastModified.Format(time.RFC850))
	}

	if resp.ContentEncoding != "" {
		w.Header().Set("Content-Encoding", resp.ContentEncoding)
	}

	if resp.Etag != "" {
		w.Header().Set("ETag", resp.Etag)
		if match := r.Header.Get("If-None-Match"); match == resp.Etag {
			w.WriteHeader(http.StatusNotModified)
			s.stats[r.URL.Path].CachedRequests++
			s.mu.Unlock()
			return
		}
	}

	s.mu.Unlock()

	w.WriteHeader(http.StatusOK)
	io.Copy(w, bytes.NewReader(resp.Content))
}

func (s *TestServer) Stats(name string) (st Stat) {
	if st, ok := s.stats[name]; ok {
		return *st
	}
	return
}

type Response struct {
	Content         []byte
	Etag            string
	LastModified    *time.Time
	ContentEncoding string
}

type Stat struct {
	AllRequests, CachedRequests int
	Requests                    []Request
}

type Request struct {
	Method string
	Header http.Header
}

func newRequest(r *http.Request) Request {
	headers := http.Header{}
	for k, v := range r.Header {
		headers[k] = append([]string{}, v...)
	}
	return Request{
		Method: r.Method,
		Header: headers,
	}
}
