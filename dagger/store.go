package dagger

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"sync"

	"github.com/google/uuid"
)

const (
	defaultStoreRoot = "$HOME/.config/dagger/routes"
)

type Store struct {
	root string

	l sync.RWMutex

	routes map[string]*RouteState

	// Various indices for fast lookups
	routesByName map[string]*RouteState
	routesByPath map[string]*RouteState
	pathsByRoute map[string][]string
}

func NewStore(root string) (*Store, error) {
	store := &Store{
		root:         root,
		routes:       make(map[string]*RouteState),
		routesByName: make(map[string]*RouteState),
		routesByPath: make(map[string]*RouteState),
		pathsByRoute: make(map[string][]string),
	}
	return store, store.loadAll()
}

func DefaultStore() (*Store, error) {
	return NewStore(os.ExpandEnv(defaultStoreRoot))
}

func (s *Store) routePath(name string) string {
	return path.Join(s.root, name, "route.json")
}

func (s *Store) loadAll() error {
	files, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		if err := s.loadRoute(f.Name()); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) loadRoute(name string) error {
	data, err := os.ReadFile(s.routePath(name))
	if err != nil {
		return err
	}
	var st RouteState
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	s.indexRoute(&st)
	return nil
}

func (s *Store) syncRoute(r *RouteState) error {
	p := s.routePath(r.Name)

	if err := os.MkdirAll(path.Dir(p), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(p, data, 0644); err != nil {
		return err
	}

	s.reindexRoute(r)

	return nil
}

func (s *Store) indexRoute(r *RouteState) {
	s.routes[r.ID] = r
	s.routesByName[r.Name] = r

	mapPath := func(i Input) {
		d, ok := i.(*dirInput)
		if !ok {
			return
		}
		s.routesByPath[d.Path] = r
		s.pathsByRoute[r.ID] = append(s.pathsByRoute[r.ID], d.Path)
	}

	mapPath(r.LayoutSource)
	for _, i := range r.Inputs {
		mapPath(i.Value)
	}
}

func (s *Store) deindexRoute(id string) {
	r, ok := s.routes[id]
	if !ok {
		return
	}
	delete(s.routes, r.ID)
	delete(s.routesByName, r.Name)

	for _, p := range s.pathsByRoute[r.ID] {
		delete(s.routesByPath, p)
	}
	delete(s.pathsByRoute, r.ID)
}

func (s *Store) reindexRoute(r *RouteState) {
	s.deindexRoute(r.ID)
	s.indexRoute(r)
}

func (s *Store) CreateRoute(ctx context.Context, st *RouteState) error {
	s.l.Lock()
	defer s.l.Unlock()

	if _, ok := s.routesByName[st.Name]; ok {
		return os.ErrExist
	}

	st.ID = uuid.New().String()
	return s.syncRoute(st)
}

type UpdateOpts struct{}

func (s *Store) UpdateRoute(ctx context.Context, r *RouteState, o *UpdateOpts) error {
	s.l.Lock()
	defer s.l.Unlock()

	return s.syncRoute(r)
}

type DeleteOpts struct{}

func (s *Store) DeleteRoute(ctx context.Context, r *RouteState, o *DeleteOpts) error {
	s.l.Lock()
	defer s.l.Unlock()

	if err := os.Remove(s.routePath(r.Name)); err != nil {
		return err
	}
	s.deindexRoute(r.ID)
	return nil
}

func (s *Store) LookupRouteByID(ctx context.Context, id string) (*RouteState, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	st, ok := s.routes[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return st, nil
}

func (s *Store) LookupRouteByName(ctx context.Context, name string) (*RouteState, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	st, ok := s.routesByName[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return st, nil
}

func (s *Store) LookupRouteByPath(ctx context.Context, path string) (*RouteState, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	st, ok := s.routesByPath[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return st, nil
}

func (s *Store) ListRoutes(ctx context.Context) ([]*RouteState, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	routes := make([]*RouteState, 0, len(s.routes))

	for _, st := range s.routes {
		routes = append(routes, st)
	}

	return routes, nil
}
