package schema

import (
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/graphql"
	"github.com/vito/progrock"
)

type Resolvers map[string]Resolver

type Resolver interface {
	_resolver()
}

type FieldResolvers interface {
	Resolver
	Fields() map[string]graphql.FieldResolveFn
	SetField(string, graphql.FieldResolveFn)
}

type ObjectResolver map[string]graphql.FieldResolveFn

func (ObjectResolver) _resolver() {}

func (r ObjectResolver) Fields() map[string]graphql.FieldResolveFn {
	return r
}

func (r ObjectResolver) SetField(name string, fn graphql.FieldResolveFn) {
	r[name] = fn
}

type IDableObjectResolver interface {
	FromID(id string) (any, error)
	ToID(any) (string, error)
	Resolver
}

func ToIDableObjectResolver[T any, I ~string](idToObject func(I) (*T, error), r ObjectResolver) IDableObjectResolver {
	return idableObjectResolver[T, I]{idToObject, r}
}

type idableObjectResolver[T any, I ~string] struct {
	idToObject func(I) (*T, error)
	ObjectResolver
}

func (r idableObjectResolver[T, I]) FromID(id string) (any, error) {
	return r.idToObject(I(id))
}

func (r idableObjectResolver[T, I]) ToID(t any) (string, error) {
	return core.ResourceToID(t)
}

type ScalarResolver struct {
	Serialize    graphql.SerializeFn
	ParseValue   graphql.ParseValueFn
	ParseLiteral graphql.ParseLiteralFn
}

func (ScalarResolver) _resolver() {}

// ToResolver transforms any function f with a *Context, a parent P and some args A that returns a Response R and an error
// into a graphql resolver graphql.FieldResolveFn.
func ToResolver[P any, A any, R any](f func(*core.Context, P, A) (R, error)) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		recorder := progrock.FromContext(p.Context)

		var args A
		argBytes, err := json.Marshal(p.Args)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal args: %w", err)
		}
		if err := json.Unmarshal(argBytes, &args); err != nil {
			return nil, fmt.Errorf("failed to unmarshal args: %w", err)
		}

		parent, ok := p.Source.(P)
		if !ok {
			parentBytes, err := json.Marshal(p.Source)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal parent: %w", err)
			}
			if err := json.Unmarshal(parentBytes, &parent); err != nil {
				return nil, fmt.Errorf("failed to unmarshal parent: %w", err)
			}
		}

		if pipelineable, ok := p.Source.(pipeline.Pipelineable); ok {
			recorder = pipelineable.PipelinePath().RecorderGroup(recorder)
			p.Context = progrock.ToContext(p.Context, recorder)
		}

		vtx, err := queryVertex(recorder, p.Info.FieldName, p.Source, args)
		if err != nil {
			return nil, err
		}

		ctx := core.Context{
			Context:       p.Context,
			ResolveParams: p,
			Vertex:        vtx,
		}

		res, err := f(&ctx, parent, args)
		if err != nil {
			vtx.Done(err)
			return nil, err
		}

		if edible, ok := any(res).(resourceid.Digestible); ok {
			dg, err := edible.Digest()
			if err != nil {
				return nil, fmt.Errorf("failed to compute digest: %w", err)
			}

			vtx.Output(dg)
		}

		vtx.Done(nil)

		return res, nil
	}
}

func PassthroughResolver(p graphql.ResolveParams) (any, error) {
	return ToResolver(func(ctx *core.Context, parent any, args any) (any, error) {
		if parent == nil {
			parent = struct{}{}
		}
		return parent, nil
	})(p)
}

func ErrResolver(err error) graphql.FieldResolveFn {
	return ToResolver(func(ctx *core.Context, parent any, args any) (any, error) {
		return nil, err
	})
}
