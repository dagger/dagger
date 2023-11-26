package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"sort"

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
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic in resolver: %v", err)
				debug.PrintStack()
				panic(err)
			}
		}()
		recorder := progrock.FromContext(p.Context)

		var args A
		argBytes, err := json.Marshal(p.Args)
		if err != nil {
			return nil, fmt.Errorf("%s failed to marshal args: %w", p.Info.FieldName, err)
		}
		if err := json.Unmarshal(argBytes, &args); err != nil {
			return nil, fmt.Errorf("%s failed to unmarshal args: %w", p.Info.FieldName, err)
		}

		parent, ok := p.Source.(P)
		if !ok {
			parentBytes, err := json.Marshal(p.Source)
			if err != nil {
				return nil, fmt.Errorf("%s failed to marshal parent: %w", p.Info.FieldName, err)
			}
			if err := json.Unmarshal(parentBytes, &parent); err != nil {
				return nil, fmt.Errorf("%s failed to unmarshal parent: %w", p.Info.FieldName, err)
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

		vtx.Done(nil)

		return res, nil
	}
}

type IDCache interface {
	GetOrInitialize(digest.Digest, func() (any, error)) (any, error)
}

func chain(parent *idproto.ID, params graphql.ResolveParams) *idproto.ID {
	chainedID := idproto.New(params.Info.ReturnType.String())

	// convert query args to ID args, by AST
	idArgs := make([]*idproto.Argument, len(params.Info.FieldASTs[0].Arguments))
	for i, arg := range params.Info.FieldASTs[0].Arguments {
		idArgs[i] = idproto.Arg(arg.Name.Value, arg.Value)
	}

	// ensure argument order does not matter
	sort.SliceStable(idArgs, func(i, j int) bool {
		return idArgs[i].Name < idArgs[j].Name
	})

	// append selector to the constructor
	chainedID.Constructor = make([]*idproto.Selector, 0, len(parent.Constructor)+1)
	copy(chainedID.Constructor, parent.Constructor)
	chainedID.Append(params.Info.FieldName, idArgs...)

	return chainedID
}

func ToCachedResolver[P core.IDable, A any, R any](cache IDCache, f func(context.Context, P, A) (R, error)) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic in resolver: %v", err)
				debug.PrintStack()
				panic(err)
			}
		}()

		parent, ok := p.Source.(P)
		if !ok {
			return nil, fmt.Errorf("%s expected source to be %T, got %T", p.Info.FieldName, parent, p.Source)
		}

		id := chain(parent.ID(), p)

		dig, err := id.Digest()
		if err != nil {
			return nil, err
		}

		log.Println("??? CACHE CHECK", dig)

		return cache.GetOrInitialize(dig, func() (any, error) {
			log.Println("!!! CACHE MISS", dig, id.String())

			recorder := progrock.FromContext(p.Context)

			var args A
			argBytes, err := json.Marshal(p.Args)
			if err != nil {
				return nil, fmt.Errorf("%s failed to marshal args: %w", p.Info.FieldName, err)
			}
			if err := json.Unmarshal(argBytes, &args); err != nil {
				return nil, fmt.Errorf("%s failed to unmarshal args: %w", p.Info.FieldName, err)
			}

			if pipelineable, ok := p.Source.(pipeline.Pipelineable); ok {
				recorder = pipelineable.PipelinePath().RecorderGroup(recorder)
				p.Context = progrock.ToContext(p.Context, recorder)
			}

			ctx := p.Context

			res, err := f(ctx, parent, args)
			if err != nil {
				return nil, err
			}

			if idable, ok := any(res).(core.IDable); ok {
				if idable.ID() == nil {
					log.Printf("!!! SETTING %T ID: %s", idable, id.String())
					idable.SetID(id)
				}
			}

			return res, nil
		})
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

func ResolveIDable[T core.Object[T]](cache *core.CacheMap[digest.Digest, any], rs Resolvers, name string, obj ObjectResolver) {
	load := loader[*T](cache)

	// Wrap each field resolver to clone the object and set its ID.
	// for name, fn := range obj {
	// 	fn := fn
	// 	// obj[name] = func(p graphql.ResolveParams) (any, error) {
	// 	// 	queryID := idproto.New(p.Info.ReturnType.Name())
	// 	// 	if idable, ok := any(p.Source).(core.IDable); ok {
	// 	// 		queryID.Constructor = idable.ID().Constructor
	// 	// 	}
	// 	// 	idArgs := make([]*idproto.Argument, len(p.Info.FieldASTs[0].Arguments))
	// 	// 	for i, arg := range p.Info.FieldASTs[0].Arguments {
	// 	// 		idArgs[i] = idproto.Arg(arg.Name.Value, arg.Value.GetValue())
	// 	// 	}
	// 	// 	sort.SliceStable(idArgs, func(i, j int) bool {
	// 	// 		return idArgs[i].Name < idArgs[j].Name
	// 	// 	})
	// 	// 	queryID.Append(p.Info.FieldName, idArgs...)

	// 	// 	dig, err := queryID.Digest()
	// 	// 	if err != nil {
	// 	// 		return nil, err
	// 	// 	}

	// 	// 	return cache.GetOrInitialize(dig, func() (any, error) {
	// 	// 		res, err := fn(p)
	// 	// 		if err != nil {
	// 	// 			return nil, err
	// 	// 		}

	// 	// 		if idable, ok := any(res).(core.IDable); ok {
	// 	// 			// if the object already has an ID, respect it
	// 	// 			if idable.ID() == nil {
	// 	// 				log.Println("!!! SETTING ID", queryID.String())

	// 	// 				idable.SetID(queryID)
	// 	// 				// TODO: store in query cache
	// 	// 			}
	// 	// 		}

	// 	// 		return res, nil
	// 	// 	})
	// 	// }
	// }

	// Add field for querying the object's ID.
	obj["id"] = ToResolver(func(ctx context.Context, obj T, args any) (_ *resourceid.ID[T], rerr error) {
		log.Println("!!! ID", obj)
		return resourceid.FromProto[T](obj.ID()), nil
	})

	// Add resolver for the type.
	rs[name] = ToIDableObjectResolver(load, obj)

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
