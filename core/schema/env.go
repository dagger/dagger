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
		dagql.Func("env", s.environment).
			Doc(`Initialize a new environment`),
	}.Install(s.srv)
	dagql.Fields[*core.Env]{
		dagql.Func("inputs", s.inputs).
			Doc("return all input values for the environment"),
		dagql.Func("input", s.binding).
			Doc("retrieve an input value by name"),
		dagql.Func("withStringInput", s.withStringInput).
			ArgDoc("name", "The name of the binding").
			ArgDoc("value", "The string value to assign to the binding").
			Doc("Create or update an input value of type string"),
	}.Install(s.srv)
	dagql.Fields[*core.Binding]{
		dagql.Func("name", s.bindingName).
			Doc("The binding name"),
		dagql.Func("typeName", s.bindingTypeName).
			Doc("The binding type"),
		dagql.Func("digest", s.bindingDigest).
			Doc("The digest of the binding value"),
	}.Install(s.srv)
	hook := core.EnvHook{Server: s.srv}
	envObjType, ok := s.srv.ObjectType(new(core.Env).Type().Name())
	if !ok {
		panic("environment type not found after dagql install")
	}
	hook.ExtendEnvType(envObjType)
	s.srv.AddInstallHook(hook)
}

func (s environmentSchema) environment(ctx context.Context, parent *core.Query, args struct{}) (*core.Env, error) {
	return core.NewEnv(), nil
}
func (s environmentSchema) inputs(ctx context.Context, env *core.Env, args struct{}) ([]*core.Binding, error) {
	return env.Inputs(), nil
}

func (s environmentSchema) binding(ctx context.Context, env *core.Env, args struct {
	Name string
}) (*core.Binding, error) {
	b, found := env.Input(args.Name)
	if found {
		return b, nil
	}
	return nil, fmt.Errorf("binding not found: %s", args.Name)
}

func (s environmentSchema) withStringInput(ctx context.Context, env *core.Env, args struct {
	Name  string
	Value string
}) (*core.Env, error) {
	return env.WithInput(args.Name, dagql.NewString(args.Value)), nil
}

func (s environmentSchema) bindingName(ctx context.Context, b *core.Binding, args struct{}) (string, error) {
	return b.Key, nil
}

func (s environmentSchema) bindingTypeName(ctx context.Context, b *core.Binding, args struct{}) (string, error) {
	return b.TypeName(), nil
}

func (s environmentSchema) bindingDigest(ctx context.Context, b *core.Binding, args struct{}) (string, error) {
	return b.Digest().String(), nil
}
