package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"

	"github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/router/internal/handler"
	"github.com/dagger/graphql"
	"github.com/dagger/graphql/gqlerrors"
	"github.com/vito/progrock"
)

type Router struct {
	schemas      map[string]ExecutableSchema
	resolvers    Resolvers
	sessionToken string

	recorder *progrock.Recorder

	s *graphql.Schema
	// mergedSchemaString is the merged schemas in SDL format, useful
	// for projects who need their dynamic schemas validated against
	// the router's current schema
	mergedSchemaString string
	h                  *handler.Handler
	l                  sync.RWMutex
}

func New(sessionToken string, recorder *progrock.Recorder) *Router {
	r := &Router{
		schemas:      make(map[string]ExecutableSchema),
		sessionToken: sessionToken,
		recorder:     recorder,
	}

	return r
}

// Do executes a query directly in the server
func (r *Router) Do(ctx context.Context, query string, opName string, variables map[string]any, data any) (*graphql.Result, error) {
	r.l.RLock()
	schema := *r.s
	r.l.RUnlock()

	params := graphql.Params{
		Context:        ctx,
		Schema:         schema,
		RequestString:  query,
		VariableValues: variables,
		OperationName:  opName,
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
	r.resolvers = merged.Resolvers()
	r.mergedSchemaString = merged.Schema()
	r.h = handler.New(&handler.Config{
		Schema: s,
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
	r.l.RLock()
	defer r.l.RUnlock()

	return r.schemas[name]
}

func (r *Router) Resolvers() Resolvers {
	r.l.Lock()
	defer r.l.Unlock()
	return r.resolvers
}

func (r *Router) MergedSchemas() string {
	r.l.RLock()
	defer r.l.RUnlock()

	return r.mergedSchemaString
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.l.RLock()
	h := r.h
	r.l.RUnlock()

	w.Header().Add("x-dagger-engine", engine.Version)

	if r.sessionToken != "" {
		username, _, ok := req.BasicAuth()
		if !ok || username != r.sessionToken {
			w.Header().Set("WWW-Authenticate", `Basic realm="Access to the Dagger engine session"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

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

	req = req.WithContext(progrock.RecorderToContext(req.Context(), r.recorder))

	mux := http.NewServeMux()
	mux.Handle("/query", h)
	mux.ServeHTTP(w, req)
}

func EngineConn(r *Router) DirectConn {
	return func(req *http.Request) (*http.Response, error) {
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, req)
		return resp.Result(), nil
	}
}

type DirectConn func(*http.Request) (*http.Response, error)

func (f DirectConn) Do(r *http.Request) (*http.Response, error) {
	return f(r)
}

func (f DirectConn) Host() string {
	return ":mem:"
}

func (f DirectConn) Close() error {
	return nil
}
