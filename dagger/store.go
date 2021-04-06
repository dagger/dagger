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
	ErrDeploymentExist    = errors.New("deployment already exists")
	ErrDeploymentNotExist = errors.New("deployment doesn't exist")
)

const (
	defaultStoreRoot = "$HOME/.dagger/store"
)

type Store struct {
	root string

	l sync.RWMutex

	// ID -> Deployment
	deployments map[string]*DeploymentState

	// Name -> Deployment
	deploymentsByName map[string]*DeploymentState

	// Path -> (ID->Deployment)
	deploymentsByPath map[string]map[string]*DeploymentState

	// ID -> (Path->{})
	pathsByDeploymentID map[string]map[string]struct{}
}

func NewStore(root string) (*Store, error) {
	store := &Store{
		root:                root,
		deployments:         make(map[string]*DeploymentState),
		deploymentsByName:   make(map[string]*DeploymentState),
		deploymentsByPath:   make(map[string]map[string]*DeploymentState),
		pathsByDeploymentID: make(map[string]map[string]struct{}),
	}
	return store, store.loadAll()
}

func DefaultStore() (*Store, error) {
	if root := os.Getenv("DAGGER_STORE"); root != "" {
		return NewStore(root)
	}

	return NewStore(os.ExpandEnv(defaultStoreRoot))
}

func (s *Store) deploymentPath(name string) string {
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
		if err := s.loadDeployment(f.Name()); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) loadDeployment(name string) error {
	data, err := os.ReadFile(s.deploymentPath(name))
	if err != nil {
		return err
	}
	var st DeploymentState
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	s.indexDeployment(&st)
	return nil
}

func (s *Store) syncDeployment(r *DeploymentState) error {
	p := s.deploymentPath(r.Name)

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

	s.reindexDeployment(r)

	return nil
}

func (s *Store) indexDeployment(r *DeploymentState) {
	s.deployments[r.ID] = r
	s.deploymentsByName[r.Name] = r

	mapPath := func(i Input) {
		if i.Type != InputTypeDir {
			return
		}
		if s.deploymentsByPath[i.Dir.Path] == nil {
			s.deploymentsByPath[i.Dir.Path] = make(map[string]*DeploymentState)
		}
		s.deploymentsByPath[i.Dir.Path][r.ID] = r

		if s.pathsByDeploymentID[r.ID] == nil {
			s.pathsByDeploymentID[r.ID] = make(map[string]struct{})
		}
		s.pathsByDeploymentID[r.ID][i.Dir.Path] = struct{}{}
	}

	mapPath(r.PlanSource)
	for _, i := range r.Inputs {
		mapPath(i.Value)
	}
}

func (s *Store) deindexDeployment(id string) {
	r, ok := s.deployments[id]
	if !ok {
		return
	}
	delete(s.deployments, r.ID)
	delete(s.deploymentsByName, r.Name)

	for p := range s.pathsByDeploymentID[r.ID] {
		delete(s.deploymentsByPath[p], r.ID)
	}
	delete(s.pathsByDeploymentID, r.ID)
}

func (s *Store) reindexDeployment(r *DeploymentState) {
	s.deindexDeployment(r.ID)
	s.indexDeployment(r)
}

func (s *Store) CreateDeployment(ctx context.Context, st *DeploymentState) error {
	s.l.Lock()
	defer s.l.Unlock()

	if _, ok := s.deploymentsByName[st.Name]; ok {
		return fmt.Errorf("%s: %w", st.Name, ErrDeploymentExist)
	}

	st.ID = uuid.New().String()
	return s.syncDeployment(st)
}

type UpdateOpts struct{}

func (s *Store) UpdateDeployment(ctx context.Context, r *DeploymentState, o *UpdateOpts) error {
	s.l.Lock()
	defer s.l.Unlock()

	return s.syncDeployment(r)
}

type DeleteOpts struct{}

func (s *Store) DeleteDeployment(ctx context.Context, r *DeploymentState, o *DeleteOpts) error {
	s.l.Lock()
	defer s.l.Unlock()

	if err := os.Remove(s.deploymentPath(r.Name)); err != nil {
		return err
	}
	s.deindexDeployment(r.ID)
	return nil
}

func (s *Store) LookupDeploymentByID(ctx context.Context, id string) (*DeploymentState, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	st, ok := s.deployments[id]
	if !ok {
		return nil, fmt.Errorf("%s: %w", id, ErrDeploymentNotExist)
	}
	return st, nil
}

func (s *Store) LookupDeploymentByName(ctx context.Context, name string) (*DeploymentState, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	st, ok := s.deploymentsByName[name]
	if !ok {
		return nil, fmt.Errorf("%s: %w", name, ErrDeploymentNotExist)
	}
	return st, nil
}

func (s *Store) LookupDeploymentByPath(ctx context.Context, path string) ([]*DeploymentState, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	res := []*DeploymentState{}

	deployments, ok := s.deploymentsByPath[path]
	if !ok {
		return res, nil
	}

	for _, d := range deployments {
		res = append(res, d)
	}

	return res, nil
}

func (s *Store) ListDeployments(ctx context.Context) ([]*DeploymentState, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	deployments := make([]*DeploymentState, 0, len(s.deployments))

	for _, st := range s.deployments {
		deployments = append(deployments, st)
	}

	return deployments, nil
}
