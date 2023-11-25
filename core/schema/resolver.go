package schema

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/idproto"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/graphql"
	"github.com/opencontainers/go-digest"
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

// TODO(vito): figure out how this changes with idproto
type IDableObjectResolver interface {
	FromID(*idproto.ID) (any, error)
	Resolver
}

func ToIDableObjectResolver[T any](idToObject func(*idproto.ID) (*T, error), r ObjectResolver) IDableObjectResolver {
	return idableObjectResolver[T]{idToObject, r}
}

type idableObjectResolver[T any] struct {
	idToObject func(*idproto.ID) (*T, error)
	ObjectResolver
}

func (r idableObjectResolver[T]) FromID(id *idproto.ID) (any, error) {
	return r.idToObject(id)
}

type ScalarResolver struct {
	Serialize    graphql.SerializeFn
	ParseValue   graphql.ParseValueFn
	ParseLiteral graphql.ParseLiteralFn
}

func (ScalarResolver) _resolver() {}

// ToResolver transforms any function f with a context.Context, a parent P and some args A that returns a Response R and an error
// into a graphql resolver graphql.FieldResolveFn.
func ToResolver[P any, A any, R any](f func(context.Context, P, A) (R, error)) graphql.FieldResolveFn {
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

		ctx := p.Context

		res, err := f(ctx, parent, args)
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
	return ToResolver(func(ctx context.Context, parent any, args any) (any, error) {
		if parent == nil {
			parent = struct{}{}
		}
		return parent, nil
	})(p)
}

func ErrResolver(err error) graphql.FieldResolveFn {
	return ToResolver(func(ctx context.Context, parent any, args any) (any, error) {
		return nil, err
	})
}

type IDable interface {
	ID() *idproto.ID
}

func ResolveIDable[T core.Object[T]](cache *core.CacheMap[digest.Digest, any], rs Resolvers, name string, obj ObjectResolver) {
	load := loader[*T](cache)

	// Add resolver for the type.
	rs[name] = ToIDableObjectResolver(load, obj)

	// Add field for querying the object's ID.
	obj["id"] = ToResolver(func(ctx context.Context, obj T, args any) (_ *resourceid.ID[T], rerr error) {
		return resourceid.FromProto[T](obj.ID()), nil
	})

	// Add resolver for its ID type.
	rs[name+"ID"] = idResolver[T]()

	// Add global constructor from ID.
	query, hasQuery := rs["Query"].(ObjectResolver)
	if !hasQuery {
		query = ObjectResolver{}
		rs["Query"] = query
	}
	loaderName := fmt.Sprintf("load%sFromID", name)
	query[loaderName] = ToResolver(func(ctx context.Context, _ any, args struct{ ID resourceid.ID[T] }) (*T, error) {
		return load(args.ID.ID)
	})
}
