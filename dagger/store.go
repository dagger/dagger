package dagger

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"

	"github.com/google/uuid"
)

const (
	defaultStoreRoot = "$HOME/.config/dagger/routes"
)

type Store struct {
	root string
}

func NewStore(root string) *Store {
	return &Store{
		root: root,
	}
}

func DefaultStore() *Store {
	return NewStore(os.ExpandEnv(defaultStoreRoot))
}

type CreateOpts struct{}

func (s *Store) CreateRoute(ctx context.Context, name string, o *CreateOpts) (*Route, error) {
	r, err := s.LookupRoute(ctx, name, &LookupOpts{})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if r != nil {
		return nil, os.ErrExist
	}
	r, err = NewRoute(
		&RouteState{
			ID:   uuid.New().String(),
			Name: name,
		},
	)
	if err != nil {
		return nil, err
	}

	return r, s.syncRoute(r)
}

type UpdateOpts struct{}

func (s *Store) UpdateRoute(ctx context.Context, r *Route, o *UpdateOpts) error {
	return s.syncRoute(r)
}

type DeleteOpts struct{}

func (s *Store) DeleteRoute(ctx context.Context, r *Route, o *DeleteOpts) error {
	return os.Remove(s.routePath(r.st.Name))
}

type LookupOpts struct{}

func (s *Store) LookupRoute(ctx context.Context, name string, o *LookupOpts) (*Route, error) {
	data, err := os.ReadFile(s.routePath(name))
	if err != nil {
		return nil, err
	}
	var st RouteState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &Route{
		st: &st,
	}, nil
}

type LoadOpts struct{}

func (s *Store) LoadRoute(ctx context.Context, id string, o *LoadOpts) (*Route, error) {
	panic("NOT IMPLEMENTED")
}

func (s *Store) ListRoutes(ctx context.Context) ([]string, error) {
	routes := []string{}

	files, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		if f.IsDir() {
			routes = append(routes, f.Name())
		}
	}

	return routes, nil
}

func (s *Store) routePath(name string) string {
	return path.Join(s.root, name, "route.json")
}

func (s *Store) syncRoute(r *Route) error {
	p := s.routePath(r.st.Name)

	if err := os.MkdirAll(path.Dir(p), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(r.st, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(p, data, 0644)
}
