package router

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sort"
	"sync"

	"github.com/dagger/cloak/playground"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/handler"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type Router struct {
	schemas         map[string]ExecutableSchema
	defaultPlatform specs.Platform

	s *graphql.Schema
	h *handler.Handler
	l sync.RWMutex
}

func New(defaultPlatform specs.Platform) *Router {
	r := &Router{
		schemas:         make(map[string]ExecutableSchema),
		defaultPlatform: defaultPlatform,
	}

	if err := r.Add(&rootSchema{}); err != nil {
		panic(err)
	}

	return r
}

func (r *Router) Add(schema ExecutableSchema) error {
	r.l.Lock()
	defer r.l.Unlock()

	// Copy the current schemas and append new schemas
	r.add(schema)
	newSchemas := []ExecutableSchema{}
	for _, s := range r.schemas {
		newSchemas = append(newSchemas, s)
	}
	sort.Slice(newSchemas, func(i, j int) bool {
		return newSchemas[i].Name() < newSchemas[j].Name()
	})

	merged, err := MergeExecutableSchemas("", newSchemas...)
	if err != nil {
		return err
	}

	s, err := compile(merged)
	if err != nil {
		return err
	}

	// Atomic swap
	r.s = s
	r.h = handler.New(&handler.Config{
		Schema: s,
		RootObjectFn: func(ctx context.Context, req *http.Request) map[string]any {
			return map[string]any{
				// FIXME: make less ugly somehow (see NOTE below)
				"__p": ParentState[struct{}]{
					Platform: r.defaultPlatform,
				},
			}
		},
	})
	return nil
}

type ParentState[T any] struct {
	Platform specs.Platform
	Val      T
}

func (p ParentState[T]) Resolve(params graphql.ResolveParams) (any, error) {
	m := make(map[string]any)
	bytes, err := json.Marshal(p.Val)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(bytes, &m); err != nil {
		return nil, err
	}
	path := params.Info.Path.AsArray()
	k := path[len(path)-1].(string)
	return m[k], nil
}

func (p ParentState[T]) MarshalJSON() ([]byte, error) {
	bytes, err := json.Marshal(p.Val)
	if err != nil {
		return nil, err
	}
	return bytes, nil
}

func (p *ParentState[T]) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &p.Val)
}

func WithVal[T, V any](s ParentState[T], v V) ParentState[V] {
	return ParentState[V]{
		Platform: s.Platform,
		Val:      v,
	}
}

func Parent[T any](obj any) ParentState[T] {
	// NOTE:	the graphql-go interface is inconsistent in that the handler forces root object to be map[string]interface, but every resolver (and the internal implementation of ExecuteParams.Root) is any...
	m, ok := obj.(map[string]any)
	if ok {
		// FIXME: make less ugly somehow (NOTE above)
		obj = m["__p"]
	}
	p, ok := obj.(ParentState[T])
	if !ok {
		// FIXME:?
		panic("invalid parent state")
	}
	return p
}

func (r *Router) add(schema ExecutableSchema) {
	// Skip adding schema if it has already been added, higher callers
	// are expected to handle checks that schemas with the same name are
	// actually equivalent
	_, ok := r.schemas[schema.Name()]
	if ok {
		return
	}

	r.schemas[schema.Name()] = schema
	for _, dep := range schema.Dependencies() {
		// TODO:(sipsma) guard against infinite recursion
		r.add(dep)
	}
}

func (r *Router) Get(name string) ExecutableSchema {
	r.l.RLock()
	defer r.l.RUnlock()

	return r.schemas[name]
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.l.RLock()
	h := r.h
	r.l.RUnlock()

	defer func() {
		if v := recover(); v != nil {
			msg := "Internal Server Error"
			switch v := v.(type) {
			case error:
				msg = v.Error()
			case string:
				msg = v
			}
			http.Error(w, msg, http.StatusInternalServerError)
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/query", h)
	mux.Handle("/", playground.Handler("Cloak Dev", "/query"))
	mux.ServeHTTP(w, req)
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
