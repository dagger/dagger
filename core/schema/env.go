package schema

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"golang.org/x/sync/errgroup"
)

type environmentSchema struct {
	*MergedSchemas

	// NOTE: this should only be used to dedupe environment load by name
	// TODO: doc subtleties and difference from below cache
	loadedEnvCache *core.CacheMap[string, *core.Environment] // env name -> env

	// TODO: doc subtleties
	envDigestCache *core.CacheMap[uint64, *core.Environment] // env digest -> env
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
			"name":        ToResolver(s.environmentName),
			"load":        ToResolver(s.load),
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
			"result":          ToResolver(s.evaluateCheckResult),
		}),
		"CheckResult": ToIDableObjectResolver(core.CheckResultID.ToCheckResult, ObjectResolver{
			"id": ToResolver(s.checkResultID),
		}),
	}
}

func (s *environmentSchema) Dependencies() []ExecutableSchema {
	return nil
}

type environmentArgs struct {
	ID core.EnvironmentID
}

func (s *environmentSchema) environment(ctx *core.Context, _ *core.Query, args environmentArgs) (*core.Environment, error) {
	return args.ID.ToEnvironment()
}

type checkArgs struct {
	ID core.CheckID
}

func (s *environmentSchema) check(ctx *core.Context, _ *core.Query, args checkArgs) (*core.Check, error) {
	return args.ID.ToCheck()
}

type checkResultArgs struct {
	ID core.CheckResultID
}

func (s *environmentSchema) checkResult(ctx *core.Context, _ *core.Query, args checkResultArgs) (*core.CheckResult, error) {
	return args.ID.ToCheckResult()
}

func (s *environmentSchema) staticCheckResult(ctx *core.Context, _ *core.Query, args core.CheckResult) (*core.CheckResult, error) {
	return &args, nil
}

func (s *environmentSchema) currentEnvironment(ctx *core.Context, _ *core.Query, args any) (*core.Environment, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if clientMetadata.EnvironmentDigest == 0 {
		return nil, fmt.Errorf("not executing in an environment")
	}

	// TODO: doc subtleties
	return s.envDigestCache.GetOrInitialize(clientMetadata.EnvironmentDigest, func() (*core.Environment, error) {
		return nil, fmt.Errorf("environment with digest %d not found", clientMetadata.EnvironmentDigest)
	})
}

func (s *environmentSchema) environmentID(ctx *core.Context, env *core.Environment, args any) (core.EnvironmentID, error) {
	return env.ID()
}

func (s *environmentSchema) environmentName(ctx *core.Context, env *core.Environment, args any) (string, error) {
	return env.Config.Name, nil
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
		env, err := core.LoadEnvironment(ctx, s.bk, s.progSockPath, s.envDigestCache, rootDir.Pipeline, s.platform, rootDir, args.ConfigPath)
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

func (s *environmentSchema) withWorkdir(ctx *core.Context, env *core.Environment, args withWorkdirArgs) (*core.Environment, error) {
	workdir, err := args.Workdir.ToDirectory()
	if err != nil {
		return nil, err
	}
	env = env.Clone()
	env.Workdir = workdir
	return env, nil
}

type withCheckArgs struct {
	ID core.CheckID
}

func (s *environmentSchema) withCheck(ctx *core.Context, env *core.Environment, args withCheckArgs) (_ *core.Environment, rerr error) {
	check, err := args.ID.ToCheck()
	if err != nil {
		return nil, err
	}
	return env.WithCheck(check, s.envDigestCache)
}

type checkByNameArgs struct {
	Name string
}

func (s *environmentSchema) checkByName(ctx *core.Context, env *core.Environment, args checkByNameArgs) (*core.Check, error) {
	for _, check := range env.Checks {
		if check.Name == args.Name {
			return check, nil
		}
	}
	return nil, fmt.Errorf("no check named %q", args.Name)
}

func (s *environmentSchema) checkID(ctx *core.Context, check *core.Check, args any) (core.CheckID, error) {
	return check.ID()
}

type withCheckNameArgs struct {
	Name string
}

func (s *environmentSchema) withCheckName(ctx *core.Context, check *core.Check, args withCheckNameArgs) (*core.Check, error) {
	return check.WithName(args.Name), nil
}

type withCheckDescriptionArgs struct {
	Description string
}

func (s *environmentSchema) withCheckDescription(ctx *core.Context, check *core.Check, args withCheckDescriptionArgs) (*core.Check, error) {
	return check.WithDescription(args.Description), nil
}

type withSubcheckArgs struct {
	ID core.CheckID
}

func (s *environmentSchema) withSubcheck(ctx *core.Context, check *core.Check, args withSubcheckArgs) (*core.Check, error) {
	subcheck, err := args.ID.ToCheck()
	if err != nil {
		return nil, err
	}
	return check.WithSubcheck(subcheck), nil
}

type withCheckContainerArgs struct {
	ID core.ContainerID
}

func (s *environmentSchema) withCheckContainer(ctx *core.Context, check *core.Check, args withCheckContainerArgs) (*core.Check, error) {
	ctr, err := args.ID.ToContainer()
	if err != nil {
		return nil, err
	}
	return check.WithUserContainer(ctr), nil
}

func (s *environmentSchema) evaluateCheckResult(ctx *core.Context, check *core.Check, _ any) (*core.CheckResult, error) {
	// TODO: set real pipeline
	return check.Result(ctx, s.bk, s.progSockPath, nil, s.envDigestCache)
}

func (s *environmentSchema) checkResultID(ctx *core.Context, checkResult *core.CheckResult, args any) (core.CheckResultID, error) {
	return checkResult.ID()
}
