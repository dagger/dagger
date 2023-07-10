package schema

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/universe"
	"github.com/dagger/graphql"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

type environmentSchema struct {
	*MergedSchemas
}

var _ ExecutableSchema = &environmentSchema{}

func (s *environmentSchema) Name() string {
	return "environment"
}

func (s *environmentSchema) Schema() string {
	return Environment
}

var environmentIDResolver = stringResolver(core.EnvironmentID(""))

var environmentCommandIDResolver = stringResolver(core.EnvironmentCommandID(""))

func (s *environmentSchema) Resolvers() Resolvers {
	return Resolvers{
		"EnvironmentID":        environmentIDResolver,
		"EnvironmentCommandID": environmentCommandIDResolver,
		"Query": ObjectResolver{
			"environment":        ToResolver(s.environment),
			"environmentCommand": ToResolver(s.environmentCommand),
		},
		"Environment": ObjectResolver{
			"id":               ToResolver(s.environmentID),
			"load":             ToResolver(s.load),
			"loadFromUniverse": ToResolver(s.loadFromUniverse),
			"name":             ToResolver(s.environmentName),
			"command":          ToResolver(s.command),
			"withCommand":      ToResolver(s.withCommand),
			"withExtension":    ToResolver(s.withExtension),
		},
		"EnvironmentCommand": ObjectResolver{
			"id":              ToResolver(s.commandID),
			"withName":        ToResolver(s.withCommandName),
			"withFlag":        ToResolver(s.withCommandFlag),
			"withResultType":  ToResolver(s.withCommandResultType),
			"withDescription": ToResolver(s.withCommandDescription),
			"setStringFlag":   ToResolver(s.setStringFlag),
			"invoke":          ToResolver(s.invoke),
		},
	}
}

func (s *environmentSchema) Dependencies() []ExecutableSchema {
	return nil
}

type environmentArgs struct {
	ID core.EnvironmentID
}

func (s *environmentSchema) environment(ctx *core.Context, parent *core.Query, args environmentArgs) (*core.Environment, error) {
	return core.NewEnvironment(args.ID)
}

func (s *environmentSchema) environmentID(ctx *core.Context, parent *core.Environment, args any) (core.EnvironmentID, error) {
	return parent.ID()
}

func (s *environmentSchema) environmentName(ctx *core.Context, parent *core.Environment, args any) (string, error) {
	return parent.Config.Name, nil
}

type loadArgs struct {
	// TODO: rename Source to RootDir
	Source     core.DirectoryID
	ConfigPath string
}

func (s *environmentSchema) load(ctx *core.Context, _ *core.Environment, args loadArgs) (*core.Environment, error) {
	rootDir, err := args.Source.ToDirectory()
	if err != nil {
		return nil, fmt.Errorf("failed to load env root directory: %w", err)
	}
	env, resolver, err := core.LoadEnvironment(ctx, s.bk, s.progSockPath, rootDir.Pipeline, s.platform, rootDir, args.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load environment: %w", err)
	}

	resolvers := make(Resolvers)
	doc, err := parser.ParseSchema(&ast.Source{Input: env.Schema})
	if err != nil {
		return nil, fmt.Errorf("failed to parse environment schema: %w: %s", err, env.Schema)
	}
	for _, def := range append(doc.Definitions, doc.Extensions...) {
		def := def
		if def.Kind != ast.Object {
			continue
		}
		existingResolver, ok := resolvers[def.Name]
		if !ok {
			existingResolver = ObjectResolver{}
		}
		objResolver, ok := existingResolver.(ObjectResolver)
		if !ok {
			return nil, fmt.Errorf("failed to load environment: resolver for %s is not an object resolver", def.Name)
		}
		for _, field := range def.Fields {
			field := field
			objResolver[field.Name] = ToResolver(func(ctx *core.Context, parent any, args any) (any, error) {
				res, err := resolver(ctx, parent, args)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve field %s: %w", field.Name, err)
				}
				return convertOutput(res, field.Type, s.MergedSchemas)
			})
		}
		resolvers[def.Name] = objResolver
	}

	envId, err := env.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get environment id: %w", err)
	}
	if err := s.addSchemas(StaticSchema(StaticSchemaParams{
		Name:      digest.FromString(string(envId)).Encoded(),
		Schema:    env.Schema,
		Resolvers: resolvers,
	})); err != nil {
		return nil, fmt.Errorf("failed to install environment schema: %w", err)
	}

	return env, nil
}

func convertOutput(output any, outputType *ast.Type, s *MergedSchemas) (any, error) {
	if outputType.Elem != nil {
		outputType = outputType.Elem
	}

	for objectName, baseResolver := range s.resolvers() {
		if objectName != outputType.Name() {
			continue
		}
		resolver, ok := baseResolver.(IDableObjectResolver)
		if !ok {
			continue
		}

		// ID-able dagger objects are serialized as their ID string across the wire
		// between the session and environment container.
		outputStr, ok := output.(string)
		if !ok {
			return nil, fmt.Errorf("expected id string output for %s", objectName)
		}
		return resolver.FromID(outputStr)
	}
	return output, nil
}

type loadFromUniverseArgs struct {
	Name string
}

var loadUniverseOnce = &sync.Once{}
var universeDirID core.DirectoryID
var loadUniverseErr error

func (s *environmentSchema) loadFromUniverse(ctx *core.Context, parent *core.Environment, args loadFromUniverseArgs) (*core.Environment, error) {
	// TODO: unpacking to a tmpdir and loading as a local dir sucks, but what's better?
	loadUniverseOnce.Do(func() {
		tempdir, err := os.MkdirTemp("", "dagger-universe")
		if err != nil {
			loadUniverseErr = err
			return
		}

		tarReader := tar.NewReader(bytes.NewReader(universe.Tar))
		for {
			header, err := tarReader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				loadUniverseErr = err
				return
			}
			if header.FileInfo().IsDir() {
				if err := os.MkdirAll(filepath.Join(tempdir, header.Name), header.FileInfo().Mode()); err != nil {
					loadUniverseErr = err
					return
				}
			} else {
				if err := os.MkdirAll(filepath.Join(tempdir, filepath.Dir(header.Name)), header.FileInfo().Mode()); err != nil {
					loadUniverseErr = err
					return
				}
				f, err := os.OpenFile(filepath.Join(tempdir, header.Name), os.O_CREATE|os.O_WRONLY, header.FileInfo().Mode())
				if err != nil {
					loadUniverseErr = err
					return
				}
				defer f.Close()
				if _, err := io.Copy(f, tarReader); err != nil {
					loadUniverseErr = err
					return
				}
			}
		}

		dir, err := core.NewHost().EngineServerDirectory(ctx, s.bk, tempdir, nil, "universe", s.platform, core.CopyFilter{})
		if err != nil {
			loadUniverseErr = err
			return
		}
		universeDirID, loadUniverseErr = dir.ID()
	})
	if loadUniverseErr != nil {
		return nil, loadUniverseErr
	}

	return s.load(ctx, parent, loadArgs{
		Source: universeDirID,
		// TODO: should be by name, not path
		ConfigPath: filepath.Join("universe", args.Name),
	})
}

type commandArgs struct {
	Name string
}

func (s *environmentSchema) command(ctx *core.Context, parent *core.Environment, args commandArgs) (*core.EnvironmentCommand, error) {
	for _, cmd := range parent.Commands {
		if cmd.Name == args.Name {
			return cmd, nil
		}
	}
	return nil, fmt.Errorf("no such command %s", args.Name)
}

type withCommandArgs struct {
	ID core.EnvironmentCommandID
}

func (s *environmentSchema) withCommand(ctx *core.Context, parent *core.Environment, args withCommandArgs) (*core.Environment, error) {
	cmd, err := args.ID.ToEnvironmentCommand()
	if err != nil {
		return nil, err
	}
	return parent.WithCommand(ctx, cmd)
}

type withExtensionArgs struct {
	ID        core.EnvironmentID
	Namespace string
}

func (s *environmentSchema) withExtension(ctx *core.Context, parent *core.Environment, args withExtensionArgs) (*core.Environment, error) {
	// TODO:
	panic("implement me")
}

type environmentCommandArgs struct {
	ID core.EnvironmentCommandID
}

func (s *environmentSchema) environmentCommand(ctx *core.Context, parent *core.Query, args environmentCommandArgs) (*core.EnvironmentCommand, error) {
	return core.NewEnvironmentCommand(args.ID)
}

func (s *environmentSchema) commandID(ctx *core.Context, parent *core.EnvironmentCommand, args any) (core.EnvironmentCommandID, error) {
	return parent.ID()
}

type withCommandNameArgs struct {
	Name string
}

func (s *environmentSchema) withCommandName(ctx *core.Context, parent *core.EnvironmentCommand, args withCommandNameArgs) (*core.EnvironmentCommand, error) {
	return parent.WithName(args.Name), nil
}

type withCommandFlagArgs struct {
	Name        string
	Description string
}

func (s *environmentSchema) withCommandFlag(ctx *core.Context, parent *core.EnvironmentCommand, args withCommandFlagArgs) (*core.EnvironmentCommand, error) {
	return parent.WithFlag(core.EnvironmentCommandFlag{
		Name:        args.Name,
		Description: args.Description,
	}), nil
}

type withCommandResultTypeArgs struct {
	Name string
}

func (s *environmentSchema) withCommandResultType(ctx *core.Context, parent *core.EnvironmentCommand, args withCommandResultTypeArgs) (*core.EnvironmentCommand, error) {
	return parent.WithResultType(args.Name), nil
}

type withCommandDescriptionArgs struct {
	Description string
}

func (s *environmentSchema) withCommandDescription(ctx *core.Context, parent *core.EnvironmentCommand, args withCommandDescriptionArgs) (*core.EnvironmentCommand, error) {
	return parent.WithDescription(args.Description), nil
}

type setStringFlagArgs struct {
	Name  string
	Value string
}

func (s *environmentSchema) setStringFlag(ctx *core.Context, parent *core.EnvironmentCommand, args setStringFlagArgs) (*core.EnvironmentCommand, error) {
	return parent.SetStringFlag(args.Name, args.Value)
}

func (s *environmentSchema) invoke(ctx *core.Context, cmd *core.EnvironmentCommand, _ any) (map[string]any, error) {
	// TODO: just for now, should namespace asap
	parentObj := s.MergedSchemas.Schema().QueryType()
	parentVal := map[string]any{}

	// find the field resolver for this command, as installed during "load" above
	var resolver Resolver
	for objectName, possibleResolver := range s.resolvers() {
		if objectName == parentObj.Name() {
			resolver = possibleResolver
		}
	}
	if resolver == nil {
		return nil, fmt.Errorf("no resolver for %s", parentObj.Name())
	}
	objResolver, ok := resolver.(ObjectResolver)
	if !ok {
		return nil, fmt.Errorf("resolver for %s is not an object resolver", parentObj.Name())
	}
	var fieldResolver graphql.FieldResolveFn
	for fieldName, possibleFieldResolver := range objResolver {
		if fieldName == cmd.Name {
			fieldResolver = possibleFieldResolver
		}
	}
	if fieldResolver == nil {
		return nil, fmt.Errorf("no field resolver for %s.%s", parentObj.Name(), cmd.Name)
	}

	// setup the inputs and invoke it
	resolveParams := graphql.ResolveParams{
		Context: ctx,
		Source:  parentVal,
		Args:    map[string]any{},
		Info: graphql.ResolveInfo{
			FieldName:  cmd.Name,
			ParentType: parentObj,
			// TODO: we don't currently use any of the other resolve info fields, but that could change
		},
	}
	for _, flag := range cmd.Flags {
		resolveParams.Args[flag.Name] = flag.SetValue
	}
	res, err := fieldResolver(resolveParams)
	if err != nil {
		return nil, err
	}

	// TODO: actual struct for this
	// return a map in the shape of the InvokeResult object in environment.graphqls
	return map[string]any{
		strings.ToLower(cmd.ResultType): res,
	}, nil
}
