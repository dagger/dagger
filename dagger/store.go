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
	storeLocation = "$HOME/.config/dagger/routes"
)

type CreateOpts struct{}

func CreateRoute(ctx context.Context, name string, o *CreateOpts) (*Route, error) {
	r, err := LookupRoute(ctx, name, &LookupOpts{})
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

	return r, syncRoute(r)
}

type UpdateOpts struct{}

func UpdateRoute(ctx context.Context, r *Route, o *UpdateOpts) error {
	return syncRoute(r)
}

type DeleteOpts struct{}

func DeleteRoute(ctx context.Context, r *Route, o *DeleteOpts) error {
	return deleteRoute(r)
}

type LookupOpts struct{}

func LookupRoute(ctx context.Context, name string, o *LookupOpts) (*Route, error) {
	st, err := loadRoute(name)
	if err != nil {
		return nil, err
	}
	return &Route{
		st: st,
	}, nil
}

type LoadOpts struct{}

func LoadRoute(ctx context.Context, id string, o *LoadOpts) (*Route, error) {
	panic("NOT IMPLEMENTED")
}

func routePath(name string) string {
	return path.Join(os.ExpandEnv(storeLocation), name+".json")
}

func syncRoute(r *Route) error {
	p := routePath(r.st.Name)

	if err := os.MkdirAll(path.Dir(p), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(r.st, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(p, data, 0644)
}

func deleteRoute(r *Route) error {
	return os.Remove(routePath(r.st.Name))
}

func loadRoute(name string) (*RouteState, error) {
	data, err := os.ReadFile(routePath(name))
	if err != nil {
		return nil, err
	}
	var st *RouteState
	if err := json.Unmarshal(data, st); err != nil {
		return nil, err
	}
	return st, nil
}
