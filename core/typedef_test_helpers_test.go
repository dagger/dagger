package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

func installTypeDefTestClasses(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*TypeDef]{Typed: &TypeDef{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Function]{Typed: &Function{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*FunctionArg]{Typed: &FunctionArg{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ListTypeDef]{Typed: &ListTypeDef{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ObjectTypeDef]{Typed: &ObjectTypeDef{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*InterfaceTypeDef]{Typed: &InterfaceTypeDef{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*InputTypeDef]{Typed: &InputTypeDef{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ScalarTypeDef]{Typed: &ScalarTypeDef{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*EnumTypeDef]{Typed: &EnumTypeDef{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*FieldTypeDef]{Typed: &FieldTypeDef{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*EnumMemberTypeDef]{Typed: &EnumMemberTypeDef{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*SourceMap]{Typed: &SourceMap{}}))

	dagql.Fields[*Query]{
		dagql.Func("typeDef", func(context.Context, *Query, struct{}) (*TypeDef, error) {
			return &TypeDef{}, nil
		}),
	}.Install(srv)

	dagql.Fields[*TypeDef]{
		dagql.Func("withKind", func(_ context.Context, def *TypeDef, args struct {
			Kind TypeDefKind
		}) (*TypeDef, error) {
			return def.WithKind(args.Kind), nil
		}),
		dagql.Func("withOptional", func(_ context.Context, def *TypeDef, args struct {
			Optional bool
		}) (*TypeDef, error) {
			return def.WithOptional(args.Optional), nil
		}),
		dagql.Func("withScalar", func(ctx context.Context, def *TypeDef, args struct {
			Name             string
			Description      string `default:""`
			SourceModuleName dagql.Optional[dagql.String]
		}) (*TypeDef, error) {
			scalar, err := newTypeDefTestObjectResultForCurrentCall(ctx, srv, &ScalarTypeDef{
				Name:             args.Name,
				OriginalName:     args.Name,
				Description:      args.Description,
				SourceModuleName: string(args.SourceModuleName.Value),
			})
			if err != nil {
				return nil, err
			}
			return def.WithScalar(scalar), nil
		}),
		dagql.Func("withListOf", func(ctx context.Context, def *TypeDef, args struct {
			ElementType TypeDefID
		}) (*TypeDef, error) {
			elem, err := args.ElementType.Load(ctx, srv)
			if err != nil {
				return nil, err
			}
			list, err := newTypeDefTestObjectResultForCurrentCall(ctx, srv, &ListTypeDef{
				ElementTypeDef: elem,
			})
			if err != nil {
				return nil, err
			}
			return def.WithListOf(list), nil
		}),
		dagql.Func("withObject", func(ctx context.Context, def *TypeDef, args struct {
			Name             string
			Description      string `default:""`
			Deprecated       *string
			SourceMap        dagql.Optional[SourceMapID]
			SourceModuleName dagql.Optional[dagql.String]
		}) (*TypeDef, error) {
			obj := NewObjectTypeDef(args.Name, args.Description, args.Deprecated)
			obj.SourceModuleName = string(args.SourceModuleName.Value)
			if args.SourceMap.Valid {
				sourceMap, err := args.SourceMap.Value.Load(ctx, srv)
				if err != nil {
					return nil, err
				}
				obj.SourceMap = dagql.NonNull(sourceMap)
			}
			objRes, err := newTypeDefTestObjectResultForCurrentCall(ctx, srv, obj)
			if err != nil {
				return nil, err
			}
			return def.WithObject(objRes), nil
		}),
		dagql.Func("withInterface", func(ctx context.Context, def *TypeDef, args struct {
			Name             string
			Description      string `default:""`
			SourceMap        dagql.Optional[SourceMapID]
			SourceModuleName dagql.Optional[dagql.String]
		}) (*TypeDef, error) {
			iface := NewInterfaceTypeDef(args.Name, args.Description)
			iface.SourceModuleName = string(args.SourceModuleName.Value)
			if args.SourceMap.Valid {
				sourceMap, err := args.SourceMap.Value.Load(ctx, srv)
				if err != nil {
					return nil, err
				}
				iface.SourceMap = dagql.NonNull(sourceMap)
			}
			ifaceRes, err := newTypeDefTestObjectResultForCurrentCall(ctx, srv, iface)
			if err != nil {
				return nil, err
			}
			return def.WithInterface(ifaceRes), nil
		}),
		dagql.Func("withEnum", func(ctx context.Context, def *TypeDef, args struct {
			Name             string
			Description      string `default:""`
			SourceMap        dagql.Optional[SourceMapID]
			SourceModuleName dagql.Optional[dagql.String]
		}) (*TypeDef, error) {
			var sourceMap dagql.ObjectResult[*SourceMap]
			if args.SourceMap.Valid {
				loaded, err := args.SourceMap.Value.Load(ctx, srv)
				if err != nil {
					return nil, err
				}
				sourceMap = loaded
			}
			enum := NewEnumTypeDef(args.Name, args.Description, sourceMap)
			enum.SourceModuleName = string(args.SourceModuleName.Value)
			enumRes, err := newTypeDefTestObjectResultForCurrentCall(ctx, srv, enum)
			if err != nil {
				return nil, err
			}
			return def.WithEnum(enumRes), nil
		}),
	}.Install(srv)
}

func newTypeDefTestObjectResultForCurrentCall[T dagql.Typed](ctx context.Context, srv *dagql.Server, self T) (dagql.ObjectResult[T], error) {
	curCall := dagql.CurrentCall(ctx)
	if curCall == nil {
		return dagql.NewObjectResultForCall(self, srv, moduleObjectTestSyntheticCall("typedefTest"+self.Type().Name(), self))
	}
	return dagql.NewObjectResultForCall(self, srv, &dagql.ResultCall{
		Kind:           curCall.Kind,
		Type:           dagql.NewResultCallType(self.Type()),
		Field:          curCall.Field,
		SyntheticOp:    curCall.SyntheticOp,
		View:           curCall.View,
		Nth:            curCall.Nth,
		EffectIDs:      append([]string(nil), curCall.EffectIDs...),
		ExtraDigests:   append([]call.ExtraDigest(nil), curCall.ExtraDigests...),
		Receiver:       curCall.Receiver,
		Module:         curCall.Module,
		Args:           append([]*dagql.ResultCallArg(nil), curCall.Args...),
		ImplicitInputs: append([]*dagql.ResultCallArg(nil), curCall.ImplicitInputs...),
	})
}

func newTypeDefTestDag(t *testing.T) *dagql.Server {
	t.Helper()
	root := &Query{}
	dag, err := dagql.NewServer(t.Context(), root)
	if err != nil {
		t.Fatalf("new typedef test dag: %v", err)
	}
	installTypeDefTestClasses(dag)
	return dag
}

func newTypeDefDetachedResult[T dagql.Typed](t *testing.T, dag *dagql.Server, op string, self T) dagql.ObjectResult[T] {
	t.Helper()
	call := moduleObjectTestSyntheticCall(op, self)
	res, err := dagql.NewObjectResultForCall(self, dag, call)
	if err != nil {
		t.Fatalf("new detached %T result: %v", self, err)
	}
	return res
}

func newTypeDefAttachedResult[T dagql.Typed](t *testing.T, ctx context.Context, cache *dagql.Cache, dag *dagql.Server, op string, self T) dagql.ObjectResult[T] {
	t.Helper()
	call := moduleObjectTestSyntheticCall(op, self)
	detached, err := dagql.NewObjectResultForCall(self, dag, call)
	if err != nil {
		t.Fatalf("new detached %T result: %v", self, err)
	}
	attachedAny, err := cache.GetOrInitCall(ctx, "typedef-test-session", dag, &dagql.CallRequest{ResultCall: call}, dagql.ValueFunc(detached))
	if err != nil {
		t.Fatalf("attach %T result: %v", self, err)
	}
	attached, ok := attachedAny.(dagql.ObjectResult[T])
	if !ok {
		t.Fatalf("attach %T result: unexpected type %T", self, attachedAny)
	}
	return attached
}
