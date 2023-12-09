package dagql

import (
	"context"
	"fmt"

	"github.com/dagger/dagql/idproto"
	"github.com/opencontainers/go-digest"
)

type Resolver interface {
	Field(string) (FieldSpec, bool)
	Resolve(context.Context, Selector) (any, error)
}

type FieldSpec struct {
	// Name is the name of the field.
	Name string

	// Args is the list of arguments that the field accepts.
	Args []ArgSpec

	// Type is the type of the field's result.
	Type Type

	// Meta indicates that the field has no impact on the field's result.
	Meta bool

	// Pure indicates that the field is a pure function of its arguments, and
	// thus can be cached indefinitely.
	Pure bool
}

type ArgSpec struct {
	// Name is the name of the argument.
	Name string
	// Type is the type of the argument.
	Type Type
}

type Type struct {
	// Named is the name of the type, if it is a named type.
	Named string // TODO likely not exhaustive
	// ListOf is the type of the elements of the list, if it is a list type.
	ListOf *Type
	// NonNull indicates that the type is non-null.
	NonNull bool
}

type Literal struct {
	*idproto.Literal
}

type Selector struct {
	Field string
	Args  map[string]Literal
}

// Per the GraphQL spec, a Node always has an ID.
type Node interface {
	Resolver

	ID() *idproto.ID
}

type Query struct {
	Selections []Selection
}

type Selection struct {
	Alias         string
	Selector      Selector
	Subselections []Selection
}

func (sel Selection) Name() string {
	if sel.Alias != "" {
		return sel.Alias
	}
	return sel.Selector.Field
}

type Server struct {
	Resolvers map[string]func(*idproto.ID, any) (Node, error)

	cache *CacheMap[digest.Digest, any]
}

func (s Server) Resolve(ctx context.Context, root Node, q Query) (map[string]any, error) {
	results := make(map[string]any, len(q.Selections))

	for _, sel := range q.Selections {
		args := make([]*idproto.Argument, 0, len(sel.Selector.Args))
		for name, val := range sel.Selector.Args {
			args = append(args, &idproto.Argument{
				Name:  name,
				Value: val.Literal,
			})
		}

		field, ok := root.Field(sel.Selector.Field)
		if !ok {
			// TODO better error
			return nil, fmt.Errorf("unknown field: %q", sel.Selector.Field)
		}

		chain := root.ID().Clone()
		chain.Constructor = append(chain.Constructor, &idproto.Selector{
			Field:   sel.Selector.Field,
			Args:    args,
			Tainted: !field.Pure,
			Meta:    field.Meta,
		})

		// TODO: should this be a full Type? feels odd to just have a TypeName...
		// i've definitely thought about this before already tho
		chain.TypeName = field.Type.Named

		digest, err := chain.Canonical().Digest()
		if err != nil {
			return nil, err
		}

		var val any
		if field.Pure && !chain.Tainted() { // TODO test !chain.Tainted(); intent is to not cache any queries that depend on a tainted input
			val, err = s.cache.GetOrInitialize(ctx, digest, func(ctx context.Context) (any, error) {
				return root.Resolve(ctx, sel.Selector)
			})
		} else {
			val, err = root.Resolve(ctx, sel.Selector)
		}
		if err != nil {
			return nil, err
		}

		if len(sel.Subselections) > 0 {
			if field.Type.Named == "" {
				// TODO better error
				return nil, fmt.Errorf("cannot select from non-node")
			}

			create, ok := s.Resolvers[field.Type.Named]
			if !ok {
				// TODO better error
				return nil, fmt.Errorf("unknown type %q", field.Type.Named)
			}

			resolver, err := create(chain, val)
			if err != nil {
				// TODO better error
				return nil, err
			}

			val, err = s.Resolve(ctx, resolver, Query{
				Selections: sel.Subselections,
			})
			if err != nil {
				return nil, err
			}
		}

		results[sel.Name()] = val
	}

	return results, nil
}

type ObjectResolver[T any] struct {
	Constructor *idproto.ID
	Self        T
	Fields      map[string]Field[T]
}

var _ Node = ObjectResolver[any]{}

func (r ObjectResolver[T]) ID() *idproto.ID {
	return r.Constructor
}

type Field[T any] struct {
	Spec FieldSpec
	Func func(ctx context.Context, self T, args map[string]Literal) (any, error)
}

func (r ObjectResolver[T]) Register(name string) (FieldSpec, bool) {
	field, ok := r.Fields[name]
	if !ok {
		return FieldSpec{}, false
	}
	return field.Spec, true
}

var _ Resolver = ObjectResolver[any]{}

func (r ObjectResolver[T]) Field(name string) (FieldSpec, bool) {
	field, ok := r.Fields[name]
	if !ok {
		return FieldSpec{}, false
	}
	return field.Spec, true
}

func (r ObjectResolver[T]) Resolve(ctx context.Context, sel Selector) (any, error) {
	field, ok := r.Fields[sel.Field]
	if !ok {
		return nil, fmt.Errorf("unknown field: %q", sel.Field)
	}
	return field.Func(ctx, r.Self, sel.Args)
}
