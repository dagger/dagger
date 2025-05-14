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
			Doc(`Initialize a new environment`).
			Experimental("Environments are not yet stabilized").
			Args(
				dagql.Arg("privileged").Doc("Give the environment the same privileges as the caller: core API including host access, current module, and dependencies"),
				dagql.Arg("writable").Doc("Allow new outputs to be declared and saved in the environment"),
			),
	}.Install(s.srv)
	dagql.Fields[*core.Env]{
		dagql.Func("inputs", s.inputs).
			Doc("return all input values for the environment"),
		dagql.Func("input", s.input).
			Doc("retrieve an input value by name"),
		dagql.Func("outputs", s.outputs).
			Doc("return all output values for the environment"),
		dagql.Func("output", s.output).
			Doc("retrieve an output value by name"),
		dagql.Func("withStringInput", s.withStringInput).
			Doc("Create or update an input value of type string").
			Args(
				dagql.Arg("name").Doc("The name of the binding"),
				dagql.Arg("value").Doc("The string value to assign to the binding"),
				dagql.Arg("description").Doc("The description of the input"),
			),
		dagql.Func("withStringOutput", s.withStringOutput).
			Doc("Create or update an input value of type string").
			Args(
				dagql.Arg("name").Doc("The name of the binding"),
				dagql.Arg("description").Doc("The description of the output"),
			),
	}.Install(s.srv)
	dagql.Fields[*core.Binding]{
		dagql.Func("name", s.bindingName).
			Doc("The binding name"),
		dagql.Func("typeName", s.bindingTypeName).
			Doc("The binding type"),
		dagql.Func("digest", s.bindingDigest).
			Doc("The digest of the binding value"),
		dagql.Func("asString", s.bindingAsString).
			Doc("The binding's string value"),
		dagql.Func("isNull", s.bindingIsNull).
			Doc("Returns true if the binding is null"),
	}.Install(s.srv)
	hook := core.EnvHook{Server: s.srv}
	envObjType, ok := s.srv.ObjectType(new(core.Env).Type().Name())
	if !ok {
		panic("environment type not found after dagql install")
	}
	hook.ExtendEnvType(envObjType)
	s.srv.AddInstallHook(hook)
}

func (s environmentSchema) environment(ctx context.Context, parent *core.Query, args struct {
	Privileged bool `default:"false"`
	Writable   bool `default:"false"`
}) (*core.Env, error) {
	env := core.NewEnv()
	if args.Privileged {
		env = env.Privileged()
	}
	if args.Writable {
		env = env.Writable()
	}
	return env, nil
}

func (s environmentSchema) inputs(ctx context.Context, env *core.Env, args struct{}) ([]*core.Binding, error) {
	return env.Inputs(), nil
}

func (s environmentSchema) input(ctx context.Context, env *core.Env, args struct {
	Name string
}) (*core.Binding, error) {
	b, found := env.Input(args.Name)
	if found {
		return b, nil
	}
	return nil, fmt.Errorf("input not found: %s", args.Name)
}

func (s environmentSchema) output(ctx context.Context, env *core.Env, args struct {
	Name string
}) (*core.Binding, error) {
	b, found := env.Output(args.Name)
	if found {
		return b, nil
	}
	return nil, fmt.Errorf("output not found: %s", args.Name)
}

func (s environmentSchema) outputs(ctx context.Context, env *core.Env, args struct{}) ([]*core.Binding, error) {
	return env.Outputs(), nil
}

func (s environmentSchema) withStringInput(ctx context.Context, env *core.Env, args struct {
	Name        string
	Value       string
	Description string
}) (*core.Env, error) {
	return env.WithInput(args.Name, dagql.NewString(args.Value), args.Description), nil
}

func (s environmentSchema) withStringOutput(ctx context.Context, env *core.Env, args struct {
	Name        string
	Description string
}) (*core.Env, error) {
	return env.WithOutput(args.Name, dagql.String(""), args.Description), nil
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

func (s environmentSchema) bindingAsString(ctx context.Context, b *core.Binding, args struct{}) (dagql.Nullable[dagql.String], error) {
	if str, ok := b.AsString(); ok {
		return dagql.NonNull[dagql.String](dagql.NewString(str)), nil
	}
	return dagql.Null[dagql.String](), nil
}

func (s environmentSchema) bindingIsNull(ctx context.Context, b *core.Binding, args struct{}) (bool, error) {
	return b.Value == nil, nil
}
