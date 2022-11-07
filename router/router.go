package router

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagger/dagger/router/internal/handler"
	"github.com/dagger/dagger/router/internal/playground"
	"github.com/dagger/dagger/sessions"
	"github.com/dagger/graphql"
	"github.com/dagger/graphql/gqlerrors"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
)

// SessionManager is a whole lot like a Buildkit *session.Manager except it
// opens a gateway client.
type SessionManager interface {
	HandleHTTPRequest(context.Context, http.ResponseWriter, *http.Request) error
	Gateway(ctx context.Context, id string) (bkgw.Client, error)
}

type Router struct {
	schemas map[string]ExecutableSchema

	gqlSchema  *graphql.Schema
	gqlHandler http.Handler
	gqlL       sync.RWMutex

	manager *sessions.Manager
}

func New(sm *sessions.Manager) *Router {
	r := &Router{
		schemas: make(map[string]ExecutableSchema),
		manager: sm,
	}

	if err := r.Add(&rootSchema{}); err != nil {
		panic(err)
	}

	return r
}

// Do executes a query directly in the server
func (r *Router) Do(ctx context.Context, query string, variables map[string]any, data any) (*graphql.Result, error) {
	r.gqlL.RLock()
	schema := *r.gqlSchema
	r.gqlL.RUnlock()

	params := graphql.Params{
		Context:        ctx,
		Schema:         schema,
		RequestString:  query,
		VariableValues: variables,
	}
	result := graphql.Do(params)
	if result.HasErrors() {
		messages := []string{}
		for _, e := range result.Errors {
			messages = append(messages, e.Message)
		}
		return nil, errors.New(strings.Join(messages, "\n"))
	}

	if data != nil {
		marshalled, err := json.Marshal(result.Data)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(marshalled, data); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (r *Router) Add(schema ExecutableSchema) error {
	r.gqlL.Lock()
	defer r.gqlL.Unlock()

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
	r.gqlSchema = s
	r.gqlHandler = handler.New(&handler.Config{
		Schema: s,
	})
	r.gqlHandler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		sid := req.URL.Query().Get("session")
		log.Println("SETTING SID", sid)
		ctx = context.WithValue(ctx, sessionIDKey{}, sid)
		handler.New(&handler.Config{
			Schema: s,
		}).ContextHandler(ctx, w, req)
	})
	return nil
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
	r.gqlL.RLock()
	defer r.gqlL.RUnlock()

	return r.schemas[name]
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.gqlL.RLock()
	queryHandler := r.gqlHandler
	r.gqlL.RUnlock()

	defer func() {
		if v := recover(); v != nil {
			msg := "Internal Server Error"
			code := http.StatusInternalServerError
			switch v := v.(type) {
			case error:
				msg = v.Error()
				if errors.As(v, &InvalidInputError{}) {
					// panics can happen on invalid input in scalar serde
					code = http.StatusBadRequest
				}
			case string:
				msg = v
			}
			res := graphql.Result{
				Errors: []gqlerrors.FormattedError{
					gqlerrors.NewFormattedError(msg),
				},
			}
			bytes, err := json.Marshal(res)
			if err != nil {
				panic(err)
			}
			http.Error(w, string(bytes), code)
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/query", queryHandler)
	mux.Handle("/session", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		err := r.manager.HandleHTTPRequest(req.Context(), w, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}))
	mux.Handle("/", playground.Handler("Dagger Dev", "/query"))
	mux.ServeHTTP(w, req)
}

func (r *Router) ServeConn(conn net.Conn) error {
	l := &singleConnListener{
		conn: conn,
	}

	s := http.Server{
		Handler:           r,
		ReadHeaderTimeout: 30 * time.Second,
	}
	return s.Serve(l)
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
