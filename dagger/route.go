package dagger

import (
	"context"

	"dagger.io/go/dagger/compiler"
)

// A deployment route
type Route struct {
	st routeState
}

func (r Route) ID() string {
	return r.st.ID
}

func (r Route) Name() string {
	return r.st.Name
}

func (r Route) LayoutSource() Input {
	return r.st.LayoutSource
}

func (r *Route) SetLayoutSource(ctx context.Context, src Input) error {
	r.st.LayoutSource = src
	return nil
}

func (r *Route) AddInput(ctx context.Context, key string, value Input) error {
	r.st.Inputs = append(r.st.Inputs, inputKV{Key: key, Value: value})
	return nil
}

// Remove all inputs at the given key, including sub-keys.
// For example RemoveInputs("foo.bar") will remove all inputs
//   at foo.bar, foo.bar.baz, etc.
func (r *Route) RemoveInputs(ctx context.Context, key string) error {
	panic("NOT IMPLEMENTED")
}

// Contents of a route serialized to a file
type routeState struct {
	// Globally unique route ID
	ID string

	// Human-friendly route name.
	// A route may have more than one name.
	// FIXME: store multiple names?
	Name string

	// Cue module containing the route layout
	// The input's top-level artifact is used as a module directory.
	LayoutSource Input

	Inputs []inputKV
}

type inputKV struct {
	Key   string
	Value Input
}

func CreateRoute(ctx context.Context, name string, o *CreateOpts) (*Route, error) {
	return &Route{
		st: routeState{
			ID:   "FIXME",
			Name: name,
		},
	}, nil
}

type CreateOpts struct{}

func DeleteRoute(ctx context.Context, o *DeleteOpts) (*Route, error) {
	panic("NOT IMPLEMENTED")
}

type DeleteOpts struct{}

func LookupRoute(name string, o *LookupOpts) (*Route, error) {
	panic("NOT IMPLEMENTED")
}

type LookupOpts struct{}

func LoadRoute(ctx context.Context, id string, o *LoadOpts) (*Route, error) {
	panic("NOT IMPLEMENTED")
}

type LoadOpts struct{}

func (r *Route) Up(ctx context.Context, o *UpOpts) error {
	panic("NOT IMPLEMENTED")
}

type UpOpts struct{}

func (r *Route) Down(ctx context.Context, o *DownOpts) error {
	panic("NOT IMPLEMENTED")
}

type DownOpts struct{}

func (r *Route) Query(ctx context.Context, expr interface{}, o *QueryOpts) (*compiler.Value, error) {
	panic("NOT IMPLEMENTED")
}

type QueryOpts struct{}
