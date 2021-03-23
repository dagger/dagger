package dagger

import (
	"context"

	"dagger.io/go/dagger/compiler"
)

// A deployment route
type Route struct {
	// Globally unique route ID
	ID string

	// Human-friendly route name.
	// A route may have more than one name.
	Name string
}

func CreateRoute(ctx context.Context, name string, o *CreateOpts) (*Route, error) {
	panic("NOT IMPLEMENTED")
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

func (r *Route) SetLayout(ctx context.Context, a *Artifact) error {
	panic("NOT IMPLEMENTED")
}

func (r *Route) Layout() (*Artifact, error) {
	panic("NOT IMPLEMENTED")
}

func (r *Route) AddInputArtifact(ctx context.Context, target string, a *Artifact) error {
	panic("NOT IMPLEMENTED")
}

func (r *Route) AddInputValue(ctx context.Context, target string, v *compiler.Value) error {
	panic("NOT IMPLEMENTED")
}

// FIXME: how does remove work? Does it require a specific file layout?
func (r *Route) RemoveInputs(ctx context.Context, target string) error {
	panic("NOT IMPLEMENTED")
}

// FIXME: connect outputs to auto-export values and artifacts.

// An artifact is a piece of data, like a source code checkout,
// binary bundle, container image, database backup etc.
//
// Artifacts can be passed as inputs, generated dynamically from
// other inputs, and received as outputs.
//
// Under the hood, an artifact is encoded as a LLB pipeline, and
// attached to the cue configuration as a
type Artifact struct {
	llb interface{}
}

func Dir(path string, include []string) *Artifact {
	var llb struct {
		Do      string
		Include []string
	}
	llb.Do = "local"
	llb.Include = include
	return &Artifact{
		llb: llb,
	}
}

func Git(remote, ref, dir string) *Artifact {
	panic("NOT IMPLEMENTED")
}

func Container(ref string) *Artifact {
	panic("NOT IMPLEMENTED")
}

func LLB(code interface{}) *Artifact {
	panic("NOT IMPLEMENTED")
}

// FIXME: manage base
// FIXME: manage inputs
// FIXME: manage outputs
