package dagger

import (
	"context"

	"dagger.io/go/dagger/compiler"
)

// A deployment route
type Route struct {
	// Globally unique route ID
	ID string
}

func CreateRoute(ctx context.Context, opts ...CreateOpt) (*Route, error) {
	panic("NOT IMPLEMENTED")
}

type CreateOpt interface{} // FIXME

func DeleteRoute(ctx context.Context, opts ...DeleteOpt) (*Route, error) {
	panic("NOT IMPLEMENTED")
}

type DeleteOpt interface{} // FIXME

func LookupRoute(name string, opts ...LookupOpt) (*Route, error) {
	panic("NOT IMPLEMENTED")
}

type LookupOpt interface{} // FIXME

func LoadRoute(ctx context.Context, id string, opts ...LoadOpt) (*Route, error) {
	panic("NOT IMPLEMENTED")
}

type LoadOpt interface{} // FIXME

func (r *Route) Up(ctx context.Context, opts ...UpOpt) error {
	panic("NOT IMPLEMENTED")
}

type UpOpt interface{} // FIXME

func (r *Route) Down(ctx context.Context, opts ...DownOpt) error {
	panic("NOT IMPLEMENTED")
}

type DownOpt interface{} // FIXME

func (r *Route) Query(ctx context.Context, expr interface{}, opts ...QueryOpt) (*compiler.Value, error) {
	panic("NOT IMPLEMENTED")
}

type QueryOpt interface{} // FIXME

// FIXME: manage base
// FIXME: manage inputs
// FIXME: manage outputs
