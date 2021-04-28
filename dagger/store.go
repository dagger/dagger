package dagger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sync"

	"github.com/google/uuid"
)

var (
	ErrEnvironmentExist    = errors.New("environment already exists")
	ErrEnvironmentNotExist = errors.New("environment doesn't exist")
)

const (
	defaultStoreRoot = "$HOME/.dagger/store"
)

type Store struct {
	root string

	l sync.RWMutex

	// ID -> Environment
	environments map[string]*EnvironmentState

	// Name -> Environment
	environmentsByName map[string]*EnvironmentState

	// Path -> (ID->Environment)
	environmentsByPath map[string]map[string]*EnvironmentState

	// ID -> (Path->{})
	pathsByEnvironmentID map[string]map[string]struct{}
}

func NewStore(root string) (*Store, error) {
	store := &Store{
		root:                 root,
		environments:         make(map[string]*EnvironmentState),
		environmentsByName:   make(map[string]*EnvironmentState),
		environmentsByPath:   make(map[string]map[string]*EnvironmentState),
		pathsByEnvironmentID: make(map[string]map[string]struct{}),
	}
	return store, store.loadAll()
}

func DefaultStore() (*Store, error) {
	if root := os.Getenv("DAGGER_STORE"); root != "" {
		return NewStore(root)
	}

	return NewStore(os.ExpandEnv(defaultStoreRoot))
}

func (s *Store) environmentPath(name string) string {
	// FIXME: rename to environment.json ?
	return path.Join(s.root, name, "deployment.json")
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
		if err := s.loadEnvironment(f.Name()); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) loadEnvironment(name string) error {
	data, err := os.ReadFile(s.environmentPath(name))
	if err != nil {
		return err
	}
	var st EnvironmentState
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	s.indexEnvironment(&st)
	return nil
}

func (s *Store) syncEnvironment(r *EnvironmentState) error {
	p := s.environmentPath(r.Name)

	if err := os.MkdirAll(path.Dir(p), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(p, data, 0600); err != nil {
		return err
	}

	s.reindexEnvironment(r)

	return nil
}

func (s *Store) indexEnvironment(r *EnvironmentState) {
	s.environments[r.ID] = r
	s.environmentsByName[r.Name] = r

	mapPath := func(i Input) {
		if i.Type != InputTypeDir {
			return
		}
		if s.environmentsByPath[i.Dir.Path] == nil {
			s.environmentsByPath[i.Dir.Path] = make(map[string]*EnvironmentState)
		}
		s.environmentsByPath[i.Dir.Path][r.ID] = r

		if s.pathsByEnvironmentID[r.ID] == nil {
			s.pathsByEnvironmentID[r.ID] = make(map[string]struct{})
		}
		s.pathsByEnvironmentID[r.ID][i.Dir.Path] = struct{}{}
	}

	mapPath(r.PlanSource)
	for _, i := range r.Inputs {
		mapPath(i.Value)
	}
}

func (s *Store) deindexEnvironment(id string) {
	r, ok := s.environments[id]
	if !ok {
		return
	}
	delete(s.environments, r.ID)
	delete(s.environmentsByName, r.Name)

	for p := range s.pathsByEnvironmentID[r.ID] {
		delete(s.environmentsByPath[p], r.ID)
	}
	delete(s.pathsByEnvironmentID, r.ID)
}

func (s *Store) reindexEnvironment(r *EnvironmentState) {
	s.deindexEnvironment(r.ID)
	s.indexEnvironment(r)
}

func (s *Store) CreateEnvironment(ctx context.Context, st *EnvironmentState) error {
	s.l.Lock()
	defer s.l.Unlock()

	if _, ok := s.environmentsByName[st.Name]; ok {
		return fmt.Errorf("%s: %w", st.Name, ErrEnvironmentExist)
	}

	st.ID = uuid.New().String()
	return s.syncEnvironment(st)
}

type UpdateOpts struct{}

func (s *Store) UpdateEnvironment(ctx context.Context, r *EnvironmentState, o *UpdateOpts) error {
	s.l.Lock()
	defer s.l.Unlock()

	return s.syncEnvironment(r)
}

type DeleteOpts struct{}

func (s *Store) DeleteEnvironment(ctx context.Context, r *EnvironmentState, o *DeleteOpts) error {
	s.l.Lock()
	defer s.l.Unlock()

	if err := os.Remove(s.environmentPath(r.Name)); err != nil {
		return err
	}
	s.deindexEnvironment(r.ID)
	return nil
}

func (s *Store) LookupEnvironmentByID(ctx context.Context, id string) (*EnvironmentState, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	st, ok := s.environments[id]
	if !ok {
		return nil, fmt.Errorf("%s: %w", id, ErrEnvironmentNotExist)
	}
	return st, nil
}

func (s *Store) LookupEnvironmentByName(ctx context.Context, name string) (*EnvironmentState, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	st, ok := s.environmentsByName[name]
	if !ok {
		return nil, fmt.Errorf("%s: %w", name, ErrEnvironmentNotExist)
	}
	return st, nil
}

func (s *Store) LookupEnvironmentByPath(ctx context.Context, path string) ([]*EnvironmentState, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	res := []*EnvironmentState{}

	environments, ok := s.environmentsByPath[path]
	if !ok {
		return res, nil
	}

	for _, d := range environments {
		res = append(res, d)
	}

	return res, nil
}

func (s *Store) ListEnvironments(ctx context.Context) ([]*EnvironmentState, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	environments := make([]*EnvironmentState, 0, len(s.environments))

	for _, st := range s.environments {
		environments = append(environments, st)
	}

	return environments, nil
}
