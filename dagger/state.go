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
	routeLocation = "$HOME/.config/dagger/routes"
)

// Contents of a route serialized to a file
type RouteState struct {
	// Globally unique route ID
	ID string `json:"id,omitempty"`

	// Human-friendly route name.
	// A route may have more than one name.
	// FIXME: store multiple names?
	Name string `json:"name,omitempty"`

	// Cue module containing the route layout
	// The input's top-level artifact is used as a module directory.
	LayoutSource Input `json:"layout,omitempty"`

	Inputs []inputKV `json:"inputs,omitempty"`
}

type inputKV struct {
	Key   string `json:"key,omitempty"`
	Value Input  `json:"value,omitempty"`
}

func (r *RouteState) SetLayoutSource(ctx context.Context, src Input) error {
	r.LayoutSource = src
	return nil
}

func (r *RouteState) AddInput(ctx context.Context, key string, value Input) error {
	r.Inputs = append(r.Inputs, inputKV{Key: key, Value: value})
	return nil
}

// Remove all inputs at the given key, including sub-keys.
// For example RemoveInputs("foo.bar") will remove all inputs
//   at foo.bar, foo.bar.baz, etc.
func (r *RouteState) RemoveInputs(ctx context.Context, key string) error {
	panic("NOT IMPLEMENTED")
}

func routePath(name string) string {
	return path.Join(os.ExpandEnv(routeLocation), name+".json")
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

func CreateRoute(ctx context.Context, name string, o *CreateOpts) (*Route, error) {
	r, err := LookupRoute(name, &LookupOpts{})
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

type CreateOpts struct{}

func DeleteRoute(ctx context.Context, o *DeleteOpts) (*Route, error) {
	panic("NOT IMPLEMENTED")
}

type DeleteOpts struct{}

func LookupRoute(name string, o *LookupOpts) (*Route, error) {
	st, err := loadRoute(name)
	if err != nil {
		return nil, err
	}
	return &Route{
		st: st,
	}, nil
}

type LookupOpts struct{}

func LoadRoute(ctx context.Context, id string, o *LoadOpts) (*Route, error) {
	panic("NOT IMPLEMENTED")
}

type LoadOpts struct{}
