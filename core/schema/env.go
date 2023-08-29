package schema

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/envconfig"
	"github.com/dagger/dagger/engine"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/zeebo/xxh3"
	"golang.org/x/sync/errgroup"
)

type environmentSchema struct {
	*MergedSchemas
	envCache *core.EnvironmentCache

	// NOTE: this should only be used to dedupe environment install specifically
	installedEnvCache *core.CacheMap[uint64, *core.Environment]
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
			"installEnvironment": ToResolver(s.installEnvironment),
			"check":              ToResolver(s.check),
			"checkResult":        ToResolver(s.checkResult),
			"staticCheckResult":  ToResolver(s.staticCheckResult),
			"currentEnvironment": ToResolver(s.currentEnvironment),
		},
		"Environment": ToIDableObjectResolver(core.EnvironmentID.Decode, ObjectResolver{
			"id":          ToResolver(s.environmentID),
			"from":        ToResolver(s.environmentFrom),
			"fromConfig":  ToResolver(s.environmentFromConfig),
			"withWorkdir": ToResolver(s.withWorkdir),
			"check":       ToResolver(s.checkByName),
			// internal apis
			"withCheck":             ToResolver(s.withCheck),
			"entrypointInput":       ToResolver(s.entrypointInput),
			"returnEntrypointValue": ToResolver(s.returnEntrypointValue),
		}),
		"Check": ToIDableObjectResolver(core.CheckID.Decode, ObjectResolver{
			"id":              ToResolver(s.checkID),
			"withName":        ToResolver(s.withCheckName),
			"withDescription": ToResolver(s.withCheckDescription),
			"withSubcheck":    ToResolver(s.withSubcheck),
			"withContainer":   ToResolver(s.withCheckContainer),
			"subchecks":       ToResolver(s.subchecks),
			"result":          ToResolver(s.evaluateCheckResult),
		}),
		"CheckResult": ToIDableObjectResolver(core.CheckResultID.Decode, ObjectResolver{
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

func (s *environmentSchema) environment(ctx *core.Context, query *core.Query, args environmentArgs) (*core.Environment, error) {
	if args.ID == "" {
		return core.NewEnvironment(s.platform, query.PipelinePath()), nil
	}
	return args.ID.Decode()
}

type checkArgs struct {
	ID core.CheckID
}

func (s *environmentSchema) check(ctx *core.Context, _ *core.Query, args checkArgs) (*core.Check, error) {
	return args.ID.Decode()
}

type checkResultArgs struct {
	ID core.CheckResultID
}

func (s *environmentSchema) checkResult(ctx *core.Context, _ *core.Query, args checkResultArgs) (*core.CheckResult, error) {
	return args.ID.Decode()
}

func (s *environmentSchema) staticCheckResult(ctx *core.Context, _ *core.Query, args core.CheckResult) (*core.CheckResult, error) {
	return &args, nil
}

func (s *environmentSchema) currentEnvironment(ctx *core.Context, _ *core.Query, _ any) (*core.Environment, error) {
	return s.envCache.CachedEnvFromContext(ctx)
}

type installArgs struct {
	ID core.EnvironmentID
}

func (s *environmentSchema) installEnvironment(ctx *core.Context, _ *core.Query, args installArgs) (bool, error) {
	env, err := args.ID.Decode()
	if err != nil {
		return false, err
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return false, err
	}

	err = s.installEnvironmentForDigest(ctx, env, clientMetadata.EnvironmentDigest)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *environmentSchema) installEnvironmentForDigest(ctx context.Context, depEnv *core.Environment, dependerEnvDigest digest.Digest) error {
	envID, err := depEnv.ID()
	if err != nil {
		return err
	}

	hash := xxh3.New()
	fmt.Fprintln(hash, envID)
	fmt.Fprintln(hash, dependerEnvDigest)
	cacheKey := hash.Sum64()

	_, err = s.installedEnvCache.GetOrInitialize(cacheKey, func() (*core.Environment, error) {
		executableSchema, err := s.envToSchema(ctx, depEnv)
		if err != nil {
			return nil, fmt.Errorf("failed to convert environment to executable schema: %w", err)
		}
		if err := s.addSchemas(dependerEnvDigest, executableSchema); err != nil {
			return nil, fmt.Errorf("failed to install environment schema: %w", err)
		}
		return depEnv, nil
	})
	return err
}

func (s *environmentSchema) installDepsCallback(ctx context.Context, env *core.Environment) error {
	envDigest, err := env.Digest()
	if err != nil {
		return err
	}

	var eg errgroup.Group
	for _, dep := range env.Dependencies {
		dep := dep
		eg.Go(func() error {
			err = s.installEnvironmentForDigest(ctx, dep, envDigest)
			if err != nil {
				return fmt.Errorf("failed to install environment dependency %q: %w", dep.Name, err)
			}
			return nil
		})
	}
	return eg.Wait()
}

func gqlObjectName(env *core.Environment) string {
	// gql object name is capitalized env name
	return strings.ToUpper(env.Name[:1]) + env.Name[1:]
}

func (s *environmentSchema) envToSchema(ctx context.Context, env *core.Environment) (ExecutableSchema, error) {
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
			env.Name: PassthroughResolver,
		},
		objName: objResolver,
	}
	schemaDoc.Extensions = append(schemaDoc.Extensions, &ast.Definition{
		Name: "Query",
		Kind: ast.Object,
		Fields: ast.FieldList{&ast.FieldDefinition{
			Name: env.Name,
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
		Name:      env.Name,
		Schema:    schemaStr,
		Resolvers: resolvers,
	}), nil
}

func (s *environmentSchema) environmentID(ctx *core.Context, env *core.Environment, args any) (core.EnvironmentID, error) {
	return env.ID()
}

type environmentFromArgs struct {
	Name                   string
	SourceDirectory        core.DirectoryID
	SDK                    envconfig.SDK `json:"sdk"`
	SourceDirectorySubpath string
	Dependencies           []core.EnvironmentID
}

func (s *environmentSchema) environmentFrom(ctx *core.Context, env *core.Environment, args environmentFromArgs) (*core.Environment, error) {
	sourceDir, err := args.SourceDirectory.Decode()
	if err != nil {
		return nil, err
	}
	var deps []*core.Environment
	for _, dep := range args.Dependencies {
		dep, err := dep.Decode()
		if err != nil {
			return nil, err
		}
		deps = append(deps, dep)
	}
	return env.From(ctx, s.bk, s.progSockPath, s.envCache, s.installDepsCallback, args.Name, sourceDir, args.SourceDirectorySubpath, args.SDK, deps)
}

type environmentFromConfigArgs struct {
	SourceDirectory core.DirectoryID
	ConfigPath      string
}

func (s *environmentSchema) environmentFromConfig(ctx *core.Context, env *core.Environment, args environmentFromConfigArgs) (*core.Environment, error) {
	sourceDir, err := args.SourceDirectory.Decode()
	if err != nil {
		return nil, err
	}
	return env.FromConfig(ctx, s.bk, s.progSockPath, s.envCache, s.installDepsCallback, sourceDir, args.ConfigPath)
}

type withWorkdirArgs struct {
	Workdir core.DirectoryID
}

func (s *environmentSchema) withWorkdir(ctx *core.Context, env *core.Environment, args withWorkdirArgs) (*core.Environment, error) {
	workdir, err := args.Workdir.Decode()
	if err != nil {
		return nil, err
	}
	return env.WithWorkdir(ctx, s.bk, s.progSockPath, workdir)
}

type checkByNameArgs struct {
	Name string
}

func (s *environmentSchema) checkByName(ctx *core.Context, env *core.Environment, args checkByNameArgs) (*core.Check, error) {
	// normalize name to camel case so user can provide alternative casing like kebab-case, same as CLI.
	wantedCheckName := strcase.ToLowerCamel(args.Name)
	for _, check := range env.Checks {
		if check.Name == wantedCheckName {
			return check, nil
		}
	}
	return nil, fmt.Errorf("no check named %q", args.Name)
}

type withCheckArgs struct {
	ID         core.CheckID
	ReturnType core.CheckEntrypointReturnType
}

func (s *environmentSchema) withCheck(ctx *core.Context, env *core.Environment, args withCheckArgs) (_ *core.Environment, rerr error) {
	check, err := args.ID.Decode()
	if err != nil {
		return nil, err
	}
	return env.WithCheck(check, args.ReturnType, s.envCache)
}

func (s *environmentSchema) entrypointInput(ctx *core.Context, env *core.Environment, _ any) (*core.EntrypointInput, error) {
	return env.EntrypointInput(ctx, s.bk)
}

type returnEntrypointValueArgs struct {
	Value string
}

func (s *environmentSchema) returnEntrypointValue(ctx *core.Context, env *core.Environment, args returnEntrypointValueArgs) (bool, error) {
	err := env.ReturnEntrypointValue(ctx, args.Value, s.bk)
	if err != nil {
		return false, err
	}
	return true, nil
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
	subcheck, err := args.ID.Decode()
	if err != nil {
		return nil, err
	}
	return check.WithSubcheck(subcheck), nil
}

type withCheckContainerArgs struct {
	ID core.ContainerID
}

func (s *environmentSchema) withCheckContainer(ctx *core.Context, check *core.Check, args withCheckContainerArgs) (*core.Check, error) {
	ctr, err := args.ID.Decode()
	if err != nil {
		return nil, err
	}
	return check.WithUserContainer(ctr), nil
}

func (s *environmentSchema) subchecks(ctx *core.Context, check *core.Check, _ any) ([]*core.Check, error) {
	// TODO: set real pipeline
	return check.GetSubchecks(ctx, s.bk, s.progSockPath, nil, s.envCache, s.installDepsCallback)
}

func (s *environmentSchema) evaluateCheckResult(ctx *core.Context, check *core.Check, _ any) (*core.CheckResult, error) {
	// TODO: set real pipeline
	return check.Result(ctx, s.bk, s.progSockPath, nil, s.envCache, s.installDepsCallback)
}

func (s *environmentSchema) checkResultID(ctx *core.Context, checkResult *core.CheckResult, args any) (core.CheckResultID, error) {
	return checkResult.ID()
}
