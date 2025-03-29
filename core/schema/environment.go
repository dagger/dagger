package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type environmentSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &environmentSchema{}

func (s environmentSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("environment", s.environment).
			Doc(`Initialize a new environment`),
	}.Install(s.srv)
	dagql.Fields[*core.Environment]{
		dagql.Func("bindings", s.bindings).
			Doc("return all bindings in the environment"),
		dagql.Func("binding", s.binding).
			Doc("retrieve a binding by name"),
	}.Install(s.srv)
	dagql.Fields[*core.Binding]{
		dagql.Func("name", s.bindingName).
			Doc("The binding name"),
		dagql.Func("type", s.bindingType).
			Doc("The binding type"),
		dagql.Func("digest", s.bindingDigest).
			Doc("The digest of the binding value"),
	}.Install(s.srv)
	hook := core.EnvironmentHook{Server: s.srv}
	envObjType, ok := s.srv.ObjectType(new(core.Environment).Type().Name())
	if !ok {
		panic("environment type not found after dagql install")
	}
	hook.ExtendEnvironmentType(envObjType)
	s.srv.AddInstallHook(hook)
}

func (s environmentSchema) environment(ctx context.Context, parent *core.Query, args struct{}) (*core.Environment, error) {
	return core.NewEnvironment(), nil
}
func (s environmentSchema) bindings(ctx context.Context, env *core.Environment, args struct{}) ([]*core.Binding, error) {
	return env.Bindings(), nil
}

func (s environmentSchema) binding(ctx context.Context, env *core.Environment, args struct {
	Name string
}) (*core.Binding, error) {
	b, found := env.Binding(args.Name)
	if found {
		return b, nil
	}
	return nil, fmt.Errorf("binding not found: %s")
}
func (s environmentSchema) bindingName(ctx context.Context, b *core.Binding, args struct{}) (string, error) {
	return b.Key, nil
}

func (s environmentSchema) bindingType(ctx context.Context, b *core.Binding, args struct{}) (string, error) {
	return b.TypeName(), nil
}

func (s environmentSchema) bindingDigest(ctx context.Context, b *core.Binding, args struct{}) (string, error) {
	return b.Digest().String(), nil
}
