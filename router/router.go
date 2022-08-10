package router

import (
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/handler"
)

type Router struct {
	schemas []ExecutableSchema

	s *graphql.Schema
	h *handler.Handler
	l sync.RWMutex
}

func New() *Router {
	r := &Router{
		s: &graphql.Schema{},
	}

	if err := r.Add(&rootSchema{}); err != nil {
		panic(err)
	}

	return r
}

func (r *Router) Add(schemas ...ExecutableSchema) error {
	r.l.Lock()
	defer r.l.Unlock()

	// Copy the current schemas and append new schemas
	newSchemas := append([]ExecutableSchema{}, r.schemas...)
	newSchemas = append(newSchemas, schemas...)

	merged, err := Merge(newSchemas...)
	if err != nil {
		return err
	}

	s, err := compile(merged)
	if err != nil {
		return err
	}

	// Atomic swap
	r.schemas = newSchemas
	r.s = s
	r.h = handler.New(&handler.Config{
		Schema:     s,
		Pretty:     true,
		Playground: true,
	})
	return nil
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.l.RLock()
	h := r.h
	r.l.RUnlock()

	h.ServeHTTP(w, req)
}

func (r *Router) ServeConn(conn net.Conn) error {
	l := &singleConnListener{
		conn: conn,
	}

	return http.Serve(l, r)
}

// converts a pre-existing net.Conn into a net.Listener that returns the conn
type singleConnListener struct {
	conn net.Conn
	l    sync.Mutex
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	l.l.Lock()
	defer l.l.Unlock()

	if l.conn == nil {
		return nil, io.ErrClosedPipe
	}
	c := l.conn
	l.conn = nil
	return c, nil
}

func (l *singleConnListener) Addr() net.Addr {
	return nil
}

func (l *singleConnListener) Close() error {
	return nil
}
