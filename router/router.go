package router

import (
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

	s, err := Stitch(newSchemas)
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
