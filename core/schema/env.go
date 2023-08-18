package schema

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"golang.org/x/sync/errgroup"
)

type environmentSchema struct {
	*MergedSchemas
	// NOTE: this should only be used for environment load; the With methods on Environment
	// don't change the name so it's not safe to assume the cache entry for the name is what
	// you want
	loadedEnvCache *core.CacheMap[string, *core.Environment] // env name -> env
}

var _ ExecutableSchema = &environmentSchema{}

func (s *environmentSchema) Name() string {
	return "environment"
}

func (s *environmentSchema) Schema() string {
	return Env
}

var environmentIDResolver = stringResolver(core.EnvironmentID(""))

var checkIDResolver = stringResolver(core.CheckID(""))

var checkResultIDResolver = stringResolver(core.CheckResultID(""))

func (s *environmentSchema) Resolvers() Resolvers {
	return Resolvers{
		"EnvironmentID": environmentIDResolver,
		"CheckID":       checkIDResolver,
		"CheckResultID": checkResultIDResolver,
		"Query": ObjectResolver{
			"environment":        ToResolver(s.environment),
			"check":              ToResolver(s.check),
			"checkResult":        ToResolver(s.checkResult),
			"staticCheckResult":  ToResolver(s.staticCheckResult),
			"currentEnvironment": ToResolver(s.currentEnvironment),
		},
		"Environment": ToIDableObjectResolver(core.EnvironmentID.ToEnvironment, ObjectResolver{
			"id":          ToResolver(s.environmentID),
			"load":        ToResolver(s.load),
			"name":        ToResolver(s.environmentName),
			"withWorkdir": ToResolver(s.withWorkdir),
			"withCheck":   ToResolver(s.withCheck),
			"check":       ToResolver(s.checkByName),
		}),
		"Check": ToIDableObjectResolver(core.CheckID.ToCheck, ObjectResolver{
			"id":              ToResolver(s.checkID),
			"withName":        ToResolver(s.withCheckName),
			"withDescription": ToResolver(s.withCheckDescription),
			"withSubcheck":    ToResolver(s.withSubcheck),
			"withContainer":   ToResolver(s.withCheckContainer),
		}),
		"CheckResult": ToIDableObjectResolver(core.CheckID.ToCheck, ObjectResolver{
			"id":       ToResolver(s.resultID),
			"withName": ToResolver(s.withResultName),
		}),
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

type checkArgs struct {
	ID core.CheckID
}

func (s *environmentSchema) check(ctx *core.Context, parent *core.Query, args checkArgs) (*core.Check, error) {
	return core.NewCheck(args.ID)
}

type checkResultArgs struct {
	ID core.CheckResultID
}

func (s *environmentSchema) checkResult(ctx *core.Context, parent *core.Query, args checkResultArgs) (*core.CheckResult, error) {
	return core.NewCheckResult(args.ID)
}

type staticCheckResultArgs struct {
	Name    string
	Success bool
	Output  string
}

func (s *environmentSchema) staticCheckResult(ctx *core.Context, parent *core.Query, args staticCheckResultArgs) (*core.CheckResult, error) {
	return core.NewStaticCheckResult(args.Name, args.Success, args.Output), nil
}

func (s *environmentSchema) currentEnvironment(ctx *core.Context, parent *core.Query, args any) (*core.Environment, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	// TODO: this is broken, using the env name as cache key gives you the original environment before any With* calls
	// Change to pass around the EnvironmentID instead
	return s.loadedEnvCache.GetOrInitialize(clientMetadata.EnvironmentName, func() (*core.Environment, error) {
		return nil, fmt.Errorf("no such environment %s", clientMetadata.EnvironmentName)
	})
}

func (s *environmentSchema) environmentID(ctx *core.Context, parent *core.Environment, args any) (core.EnvironmentID, error) {
	return parent.ID()
}

func (s *environmentSchema) environmentName(ctx *core.Context, parent *core.Environment, args any) (string, error) {
	return parent.Config.Name, nil
}

type loadArgs struct {
	EnvironmentDirectory core.DirectoryID
	ConfigPath           string
}

func (s *environmentSchema) load(ctx *core.Context, _ *core.Environment, args loadArgs) (*core.Environment, error) {
	rootDir, err := args.EnvironmentDirectory.ToDirectory()
	if err != nil {
		return nil, fmt.Errorf("failed to load env root directory: %w", err)
	}

	envCfg, err := core.LoadEnvironmentConfig(ctx, s.bk, rootDir, args.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load environment config: %w", err)
	}

	var eg errgroup.Group
	for _, dep := range envCfg.Dependencies {
		dep := dep
		// TODO: currently just assuming that all deps are local and that they all share the same root
		depConfigPath := filepath.Join(filepath.Dir(args.ConfigPath), dep)
		eg.Go(func() error {
			_, err := s.load(ctx, nil, loadArgs{EnvironmentDirectory: args.EnvironmentDirectory, ConfigPath: depConfigPath})
			if err != nil {
				return fmt.Errorf("failed to load environment dependency %q: %w", dep, err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("failed to load environment dependencies: %w", err)
	}

	return s.loadedEnvCache.GetOrInitialize(envCfg.Name, func() (*core.Environment, error) {
		env, err := core.LoadEnvironment(ctx, s.bk, s.progSockPath, rootDir.Pipeline, s.platform, rootDir, args.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load environment: %w", err)
		}
		executableSchema, err := s.envToSchema(ctx, env)
		if err != nil {
			return nil, fmt.Errorf("failed to convert environment to executable schema: %w", err)
		}
		if err := s.addSchemas(executableSchema); err != nil {
			return nil, fmt.Errorf("failed to install environment schema: %w", err)
		}
		return env, nil
	})
}

func gqlObjectName(env *core.Environment) string {
	// gql object name is capitalized env name
	return strings.ToUpper(env.Config.Name[:1]) + env.Config.Name[1:]
}

func (s *environmentSchema) envToSchema(ctx *core.Context, env *core.Environment) (ExecutableSchema, error) {
	objName := gqlObjectName(env)

	schemaDoc := &ast.SchemaDocument{}
	objDef := &ast.Definition{
		Name: objName,
		Kind: ast.Object,
	}
	objResolver := ObjectResolver{}

	// checks
	for _, check := range env.Checks {
		check := check
		objResolver[check.Name] = ToResolver(func(ctx *core.Context, _ any, _ any) (*core.Check, error) {
			return check, nil
		})

		fieldDef := &ast.FieldDefinition{
			Name:        check.Name,
			Description: check.Description,
			Type: &ast.Type{
				NamedType: "Check",
				NonNull:   true,
			},
		}
		objDef.Fields = append(objDef.Fields, fieldDef)
	}

	// extend Query root with a field for this environment's object, which
	// will have fields for all the different entrypoints
	resolvers := Resolvers{
		"Query": ObjectResolver{
			env.Config.Name: PassthroughResolver,
		},
		objName: objResolver,
	}
	schemaDoc.Extensions = append(schemaDoc.Extensions, &ast.Definition{
		Name: "Query",
		Kind: ast.Object,
		Fields: ast.FieldList{&ast.FieldDefinition{
			// field is just the env name, object type is the capitalized env name (objName)
			Name: env.Config.Name,
			// TODO: Description
			Type: &ast.Type{
				NamedType: objName,
				NonNull:   true,
			},
		}},
	})
	schemaDoc.Definitions = append(schemaDoc.Definitions, objDef)

	buf := &bytes.Buffer{}
	formatter.NewFormatter(buf).FormatSchemaDocument(schemaDoc)
	schemaStr := buf.String()

	return StaticSchema(StaticSchemaParams{
		Name:      env.Config.Name,
		Schema:    schemaStr,
		Resolvers: resolvers,
	}), nil
}

type withWorkdirArgs struct {
	Workdir core.DirectoryID
}

func (s *environmentSchema) withWorkdir(ctx *core.Context, parent *core.Environment, args withWorkdirArgs) (*core.Environment, error) {
	workdir, err := args.Workdir.ToDirectory()
	if err != nil {
		return nil, err
	}
	parent = parent.Clone()
	parent.Workdir = workdir
	return parent, nil
}

type withCheckArgs struct {
	ID core.CheckID
}

func (s *environmentSchema) withCheck(ctx *core.Context, parent *core.Environment, args withCheckArgs) (*core.Environment, error) {
	check, err := args.ID.ToCheck()
	if err != nil {
		return nil, err
	}
	// TODO: set real pipeline
	return parent.WithCheck(ctx, s.bk, s.progSockPath, nil, check)
}

type checkByNameArgs struct {
	Name string
}

func (s *environmentSchema) checkByName(ctx *core.Context, parent *core.Environment, args checkByNameArgs) (*core.Check, error) {
	for _, check := range parent.Checks {
		if check.Name == args.Name {
			return check, nil
		}
	}
	return nil, fmt.Errorf("no check named %q", args.Name)
}

func (s *environmentSchema) checkID(ctx *core.Context, parent *core.Check, args any) (core.CheckID, error) {
	return parent.ID()
}

type withCheckNameArgs struct {
	Name string
}

func (s *environmentSchema) withCheckName(ctx *core.Context, parent *core.Check, args withCheckNameArgs) (*core.Check, error) {
	return parent.WithName(args.Name), nil
}

type withCheckDescriptionArgs struct {
	Description string
}

func (s *environmentSchema) withCheckDescription(ctx *core.Context, parent *core.Check, args withCheckDescriptionArgs) (*core.Check, error) {
	return parent.WithDescription(args.Description), nil
}

type withSubcheckArgs struct {
	ID core.CheckID
}

func (s *environmentSchema) withSubcheck(ctx *core.Context, parent *core.Check, args withSubcheckArgs) (*core.Check, error) {
	return parent.WithSubcheck(args.ID)
}

type withCheckContainerArgs struct {
	ID core.ContainerID
}

func (s *environmentSchema) withCheckContainer(ctx *core.Context, parent *core.Check, args withCheckContainerArgs) (*core.Check, error) {
	return parent.WithContainer(args.ID)
}

func (s *environmentSchema) resultID(ctx *core.Context, result *core.CheckResult, _ any) (core.CheckResultID, error) {
	return result.ID()
}

func (s *environmentSchema) withResultName(
	ctx *core.Context,
	result *core.CheckResult,
	args struct{ Name string },
) (*core.CheckResult, error) {
	result = result.Clone()
	result.Name = args.Name
	return result, nil
}

func (s *environmentSchema) getEnvFieldResolver(ctx context.Context, envName, fieldName string) (graphql.FieldResolveFn, *graphql.ResolveParams, error) {
	// TODO: don't hardcode
	envObjName := strings.ToUpper(envName[:1]) + envName[1:]

	var resolver Resolver
	for objectName, possibleResolver := range s.resolvers() {
		if objectName == envObjName {
			resolver = possibleResolver
		}
	}
	if resolver == nil {
		return nil, nil, fmt.Errorf("no resolver for %s", envObjName)
	}
	objResolver, ok := resolver.(ObjectResolver)
	if !ok {
		return nil, nil, fmt.Errorf("resolver for %s is not an object resolver", envObjName)
	}
	var fieldResolver graphql.FieldResolveFn
	for possibleFieldName, possibleFieldResolver := range objResolver {
		if possibleFieldName == fieldName {
			fieldResolver = possibleFieldResolver
		}
	}
	if fieldResolver == nil {
		return nil, nil, fmt.Errorf("no field resolver for %s.%s", envObjName, fieldName)
	}

	return fieldResolver, &graphql.ResolveParams{
		Context: ctx,
		Source:  struct{}{}, // TODO: could support data fields too
		Args:    map[string]any{},
		Info: graphql.ResolveInfo{
			FieldName:  fieldName,
			ParentType: s.MergedSchemas.Schema().Type(envObjName),
			// TODO: we don't currently use any of the other resolve info fields, but that could change
		},
	}, nil
}
