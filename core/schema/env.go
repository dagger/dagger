package schema

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/universe"
	"github.com/dagger/graphql"
	"github.com/moby/buildkit/util/bklog"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"golang.org/x/sync/errgroup"
)

type environmentSchema struct {
	*MergedSchemas
	loadedEnvCache *core.CacheMap[string, *core.Environment] // env name -> env
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

var environmentCheckIDResolver = stringResolver(core.EnvironmentCheckID(""))

var environmentShellIDResolver = stringResolver(core.EnvironmentShellID(""))

var environmentFunctionIDResolver = stringResolver(core.EnvironmentFunctionID(""))

func (s *environmentSchema) Resolvers() Resolvers {
	return Resolvers{
		"EnvironmentID":         environmentIDResolver,
		"EnvironmentCommandID":  environmentCommandIDResolver,
		"EnvironmentCheckID":    environmentCheckIDResolver,
		"EnvironmentShellID":    environmentShellIDResolver,
		"EnvironmentFunctionID": environmentFunctionIDResolver,
		"Query": ObjectResolver{
			"environment":         ToResolver(s.environment),
			"environmentCommand":  ToResolver(s.environmentCommand),
			"environmentCheck":    ToResolver(s.environmentCheck),
			"environmentShell":    ToResolver(s.environmentShell),
			"environmentFunction": ToResolver(s.environmentFunction),
			// TODO:
			"loadUniverse": ToResolver(s.loadUniverse),
		},
		"Environment": ObjectResolver{
			"id":            ToResolver(s.environmentID),
			"load":          ToResolver(s.load),
			"name":          ToResolver(s.environmentName),
			"command":       ToResolver(s.command),
			"withCommand":   ToResolver(s.withCommand),
			"withCheck":     ToResolver(s.withCheck),
			"withShell":     ToResolver(s.withShell),
			"withExtension": ToResolver(s.withExtension),
			"withFunction":  ToResolver(s.withFunction),
		},
		"EnvironmentFunction": ObjectResolver{
			"id":              ToResolver(s.functionID),
			"withName":        ToResolver(s.withFunctionName),
			"withDescription": ToResolver(s.withFunctionDescription),
			"withArg":         ToResolver(s.withFunctionArg),
			"withResultType":  ToResolver(s.withFunctionResultType),
		},
		"EnvironmentCommand": ObjectResolver{
			"id":              ToResolver(s.commandID),
			"withName":        ToResolver(s.withCommandName),
			"withDescription": ToResolver(s.withCommandDescription),
			"withFlag":        ToResolver(s.withCommandFlag),
			"withResultType":  ToResolver(s.withCommandResultType),
			"setStringFlag":   ToResolver(s.setCommandStringFlag),
			"invoke":          ToResolver(s.invokeCommand),
		},
		"EnvironmentCheck": ObjectResolver{
			"id":              ToResolver(s.checkID),
			"subchecks":       ToResolver(s.subchecks),
			"withSubcheck":    ToResolver(s.withSubcheck),
			"withName":        ToResolver(s.withCheckName),
			"withDescription": ToResolver(s.withCheckDescription),
			"withFlag":        ToResolver(s.withCheckFlag),
			"setStringFlag":   ToResolver(s.setCheckStringFlag),
			"result":          ToResolver(s.checkResult),
		},
		"EnvironmentShell": ObjectResolver{
			"id":              ToResolver(s.shellID),
			"withName":        ToResolver(s.withShellName),
			"withDescription": ToResolver(s.withShellDescription),
			"withFlag":        ToResolver(s.withShellFlag),
			"setStringFlag":   ToResolver(s.setShellStringFlag),
			"endpoint":        ToResolver(s.shellEndpoint),
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
			_, err := s.load(ctx, nil, loadArgs{Source: args.Source, ConfigPath: depConfigPath})
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
		env, fieldResolver, err := core.LoadEnvironment(ctx, s.bk, s.progSockPath, rootDir.Pipeline, s.platform, rootDir, args.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load environment: %w", err)
		}

		executableSchema, err := s.envToExecutableSchema(ctx, env, fieldResolver)
		if err != nil {
			return nil, fmt.Errorf("failed to convert environment to executable schema: %w", err)
		}
		if err := s.addSchemas(executableSchema); err != nil {
			return nil, fmt.Errorf("failed to install environment schema: %w", err)
		}
		return env, nil
	})
}

func (s *environmentSchema) envToExecutableSchema(ctx *core.Context, env *core.Environment, fieldResolver core.Resolver) (ExecutableSchema, error) {
	doc, err := parser.ParseSchema(&ast.Source{Input: env.Schema})
	if err != nil {
		return nil, fmt.Errorf("failed to parse environment schema: %w: %s", err, env.Schema)
	}
	objName, err := env.GQLObjectName()
	if err != nil {
		return nil, fmt.Errorf("failed to get environment object name: %w", err)
	}
	def := doc.Definitions.ForName(objName)
	if def == nil {
		return nil, fmt.Errorf("failed to find environment object %q in schema", objName)
	}

	objResolver := ObjectResolver{}
	for _, field := range def.Fields {
		field := field
		objResolver[field.Name] = ToResolver(func(ctx *core.Context, parent any, args any) (any, error) {
			res, err := fieldResolver(ctx, parent, args)
			// don't check err yet, convert output may do some handling of that
			res, err = convertOutput(res, err, field.Type, s.MergedSchemas)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve field %s: %w", field.Name, err)
			}
			return res, nil
		})
	}
	resolvers := Resolvers{
		"Query": ObjectResolver{
			env.Config.Name: PassthroughResolver,
		},
		def.Name: objResolver,
	}

	return StaticSchema(StaticSchemaParams{
		Name:      env.Config.Name,
		Schema:    env.Schema,
		Resolvers: resolvers,
	}), nil
}

func convertOutput(rawOutput any, resErr error, schemaOutputType *ast.Type, s *MergedSchemas) (any, error) {
	if schemaOutputType.Elem != nil {
		schemaOutputType = schemaOutputType.Elem
	}

	// TODO: avoid hardcoding type names amap
	if schemaOutputType.Name() == "EnvironmentCheck" {
		checkRes := &core.EnvironmentCheckResult{}
		if resErr != nil {
			checkRes.Success = false
			// TODO: forcing users to include all relevent error output in the error/exception is probably annoying
			execErr := new(buildkit.ExecError)
			if errors.As(resErr, &execErr) {
				// TODO: stdout and then stderr is weird, need interleaved stream
				checkRes.Output = strings.Join([]string{execErr.Stdout, execErr.Stderr}, "\n")
			} else {
				return nil, fmt.Errorf("failed to execute check: %w", resErr)
			}
			return checkRes, nil
		}
		checkRes.Success = true
		output, ok := rawOutput.(string)
		if ok {
			checkRes.Output = output
		}
		return checkRes, nil
	}
	if resErr != nil {
		return nil, resErr
	}

	// see if the output type needs to be converted from an id to a dagger object (container, directory, etc)
	for objectName, baseResolver := range s.resolvers() {
		if objectName != schemaOutputType.Name() {
			continue
		}
		resolver, ok := baseResolver.(IDableObjectResolver)
		if !ok {
			continue
		}

		// ID-able dagger objects are serialized as their ID string across the wire
		// between the session and environment container.
		outputStr, ok := rawOutput.(string)
		if !ok {
			return nil, fmt.Errorf("expected id string output for %s, got %T", objectName, rawOutput)
		}
		return resolver.FromID(outputStr)
	}
	return rawOutput, nil
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

type withCheckArgs struct {
	ID core.EnvironmentCheckID
}

func (s *environmentSchema) withCheck(ctx *core.Context, parent *core.Environment, args withCheckArgs) (*core.Environment, error) {
	cmd, err := args.ID.ToEnvironmentCheck()
	if err != nil {
		return nil, err
	}
	return parent.WithCheck(ctx, cmd)
}

type withShellArgs struct {
	ID core.EnvironmentShellID
}

func (s *environmentSchema) withShell(ctx *core.Context, parent *core.Environment, args withShellArgs) (*core.Environment, error) {
	cmd, err := args.ID.ToEnvironmentShell()
	if err != nil {
		return nil, err
	}
	return parent.WithShell(ctx, cmd)
}

type withFunctionArgs struct {
	ID core.EnvironmentFunctionID
}

func (s *environmentSchema) withFunction(ctx *core.Context, parent *core.Environment, args withFunctionArgs) (*core.Environment, error) {
	cmd, err := args.ID.ToEnvironmentFunction()
	if err != nil {
		return nil, err
	}
	return parent.WithFunction(ctx, cmd)
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

type environmentCheckArgs struct {
	ID core.EnvironmentCheckID
}

func (s *environmentSchema) environmentCheck(ctx *core.Context, parent *core.Query, args environmentCheckArgs) (*core.EnvironmentCheck, error) {
	return core.NewEnvironmentCheck(args.ID)
}

type environmentShellArgs struct {
	ID core.EnvironmentShellID
}

func (s *environmentSchema) environmentShell(ctx *core.Context, parent *core.Query, args environmentShellArgs) (*core.EnvironmentShell, error) {
	return core.NewEnvironmentShell(args.ID)
}

type environmentFunctionArgs struct {
	ID core.EnvironmentFunctionID
}

func (s *environmentSchema) environmentFunction(ctx *core.Context, parent *core.Query, args environmentFunctionArgs) (*core.EnvironmentFunction, error) {
	return core.NewEnvironmentFunction(args.ID)
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

type setCommandStringFlagArgs struct {
	Name  string
	Value string
}

func (s *environmentSchema) setCommandStringFlag(ctx *core.Context, parent *core.EnvironmentCommand, args setCommandStringFlagArgs) (*core.EnvironmentCommand, error) {
	return parent.SetStringFlag(args.Name, args.Value)
}

func (s *environmentSchema) invokeCommand(ctx *core.Context, cmd *core.EnvironmentCommand, _ any) (map[string]any, error) {
	// find the object resolver for the command's environment
	var resolver Resolver
	for objectName, possibleResolver := range s.resolvers() {
		if objectName == cmd.ParentObjectName {
			resolver = possibleResolver
		}
	}
	if resolver == nil {
		return nil, fmt.Errorf("no resolver for %s", cmd.ParentObjectName)
	}
	objResolver, ok := resolver.(ObjectResolver)
	if !ok {
		return nil, fmt.Errorf("resolver for %s is not an object resolver", cmd.ParentObjectName)
	}
	var fieldResolver graphql.FieldResolveFn
	for fieldName, possibleFieldResolver := range objResolver {
		if fieldName == cmd.Name {
			fieldResolver = possibleFieldResolver
		}
	}
	if fieldResolver == nil {
		return nil, fmt.Errorf("no field resolver for %s.%s", cmd.ParentObjectName, cmd.Name)
	}

	// setup the inputs and invoke it
	resolveParams := graphql.ResolveParams{
		Context: ctx,
		Source:  struct{}{}, // TODO: could support data fields too
		Args:    map[string]any{},
		Info: graphql.ResolveInfo{
			FieldName:  cmd.Name,
			ParentType: s.MergedSchemas.Schema().Type(cmd.ParentObjectName),
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

func (s *environmentSchema) checkID(ctx *core.Context, parent *core.EnvironmentCheck, args any) (core.EnvironmentCheckID, error) {
	return parent.ID()
}

func (s *environmentSchema) subchecks(ctx *core.Context, parent *core.EnvironmentCheck, args any) ([]*core.EnvironmentCheck, error) {
	var subchecks []*core.EnvironmentCheck
	for _, subcheckID := range parent.Subchecks {
		subcheck, err := core.NewEnvironmentCheck(subcheckID)
		if err != nil {
			return nil, err
		}
		subchecks = append(subchecks, subcheck)
	}
	return subchecks, nil
}

type withSubcheckArgs struct {
	ID core.EnvironmentCheckID
}

func (s *environmentSchema) withSubcheck(ctx *core.Context, parent *core.EnvironmentCheck, args withSubcheckArgs) (*core.EnvironmentCheck, error) {
	subcheck, err := core.NewEnvironmentCheck(args.ID)
	if err != nil {
		return nil, err
	}

	return parent.WithSubcheck(subcheck)
}

type withCheckNameArgs struct {
	Name string
}

func (s *environmentSchema) withCheckName(ctx *core.Context, parent *core.EnvironmentCheck, args withCheckNameArgs) (*core.EnvironmentCheck, error) {
	return parent.WithName(args.Name), nil
}

type withCheckDescriptionArgs struct {
	Description string
}

func (s *environmentSchema) withCheckDescription(ctx *core.Context, parent *core.EnvironmentCheck, args withCheckDescriptionArgs) (*core.EnvironmentCheck, error) {
	return parent.WithDescription(args.Description), nil
}

type withCheckFlagArgs struct {
	Name        string
	Description string
}

func (s *environmentSchema) withCheckFlag(ctx *core.Context, parent *core.EnvironmentCheck, args withCheckFlagArgs) (*core.EnvironmentCheck, error) {
	return parent.WithFlag(core.EnvironmentCheckFlag{
		Name:        args.Name,
		Description: args.Description,
	}), nil
}

type setCheckStringFlagArgs struct {
	Name  string
	Value string
}

func (s *environmentSchema) setCheckStringFlag(ctx *core.Context, parent *core.EnvironmentCheck, args setCheckStringFlagArgs) (*core.EnvironmentCheck, error) {
	return parent.SetStringFlag(args.Name, args.Value)
}

func (s *environmentSchema) checkResult(ctx *core.Context, check *core.EnvironmentCheck, _ any) ([]*core.EnvironmentCheckResult, error) {
	if len(check.Subchecks) > 0 {
		// run them in parallel instead
		var eg errgroup.Group
		results := make([]*core.EnvironmentCheckResult, len(check.Subchecks))
		for i, subcheckID := range check.Subchecks {
			i := i
			subcheck, err := core.NewEnvironmentCheck(subcheckID)
			if err != nil {
				return nil, err
			}
			eg.Go(func() error {
				res, err := s.runCheck(ctx, subcheck)
				if err != nil {
					return err
				}
				results[i] = res
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, err
		}
		return results, nil
	}

	/* TODO: codegen clients currently request every field when list of objects are returned, so we need to avoid doing expensive work
	// here, right? Or if not then simplify this
	// Or maybe graphql-go lets you return structs with methods that match the field names?
	checkID, err := check.ID()
	if err != nil {
		return nil, err
	}
	return []*core.EnvironmentCheckResult{{ParentCheck: checkID}}, nil
	*/

	res, err := s.runCheck(ctx, check)
	if err != nil {
		return nil, err
	}
	return []*core.EnvironmentCheckResult{res}, nil
}

// private helper, not in schema
func (s *environmentSchema) runCheck(ctx *core.Context, check *core.EnvironmentCheck) (*core.EnvironmentCheckResult, error) {
	// find the object resolver for the check's environment
	var resolver Resolver
	for objectName, possibleResolver := range s.resolvers() {
		if objectName == check.ParentObjectName {
			resolver = possibleResolver
		}
	}
	if resolver == nil {
		return nil, fmt.Errorf("no resolver for %q", check.ParentObjectName)
	}
	objResolver, ok := resolver.(ObjectResolver)
	if !ok {
		return nil, fmt.Errorf("resolver for %s is not an object resolver", check.ParentObjectName)
	}
	var fieldResolver graphql.FieldResolveFn
	for fieldName, possibleFieldResolver := range objResolver {
		if fieldName == check.Name {
			fieldResolver = possibleFieldResolver
		}
	}
	if fieldResolver == nil {
		return nil, fmt.Errorf("no field resolver for %s.%s", check.ParentObjectName, check.Name)
	}

	// setup the inputs and invoke it
	resolveParams := graphql.ResolveParams{
		Context: ctx,
		Source:  struct{}{}, // TODO: could support data fields too
		Args:    map[string]any{},
		Info: graphql.ResolveInfo{
			FieldName:  check.Name,
			ParentType: s.MergedSchemas.Schema().Type(check.ParentObjectName),
			// TODO: we don't currently use any of the other resolve info fields, but that could change
		},
	}
	for _, flag := range check.Flags {
		resolveParams.Args[flag.Name] = flag.SetValue
	}
	res, err := fieldResolver(resolveParams)
	if err != nil {
		return nil, err
	}

	// all the result type handling is done in convertOutput above
	checkRes, ok := res.(*core.EnvironmentCheckResult)
	if !ok {
		return nil, fmt.Errorf("unexpected result type %T from check resolver", res)
	}
	checkRes.Name = check.Name
	return checkRes, nil
}

func (s *environmentSchema) shellID(ctx *core.Context, parent *core.EnvironmentShell, args any) (core.EnvironmentShellID, error) {
	return parent.ID()
}

type withShellNameArgs struct {
	Name string
}

func (s *environmentSchema) withShellName(ctx *core.Context, parent *core.EnvironmentShell, args withShellNameArgs) (*core.EnvironmentShell, error) {
	return parent.WithName(args.Name), nil
}

type withShellDescriptionArgs struct {
	Description string
}

func (s *environmentSchema) withShellDescription(ctx *core.Context, parent *core.EnvironmentShell, args withShellDescriptionArgs) (*core.EnvironmentShell, error) {
	return parent.WithDescription(args.Description), nil
}

type withShellFlagArgs struct {
	Name        string
	Description string
}

func (s *environmentSchema) withShellFlag(ctx *core.Context, parent *core.EnvironmentShell, args withShellFlagArgs) (*core.EnvironmentShell, error) {
	return parent.WithFlag(core.EnvironmentShellFlag{
		Name:        args.Name,
		Description: args.Description,
	}), nil
}

type setShellStringFlagArgs struct {
	Name  string
	Value string
}

func (s *environmentSchema) setShellStringFlag(ctx *core.Context, parent *core.EnvironmentShell, args setShellStringFlagArgs) (*core.EnvironmentShell, error) {
	return parent.SetStringFlag(args.Name, args.Value)
}

func (s *environmentSchema) shellEndpoint(ctx *core.Context, shell *core.EnvironmentShell, args any) (string, error) {
	// find the object resolver for the shell's environment
	var resolver Resolver
	for objectName, possibleResolver := range s.resolvers() {
		if objectName == shell.ParentObjectName {
			resolver = possibleResolver
		}
	}
	if resolver == nil {
		return "", fmt.Errorf("no resolver for %s", shell.ParentObjectName)
	}
	objResolver, ok := resolver.(ObjectResolver)
	if !ok {
		return "", fmt.Errorf("resolver for %s is not an object resolver", shell.ParentObjectName)
	}
	var fieldResolver graphql.FieldResolveFn
	for fieldName, possibleFieldResolver := range objResolver {
		if fieldName == shell.Name {
			fieldResolver = possibleFieldResolver
		}
	}
	if fieldResolver == nil {
		return "", fmt.Errorf("no field resolver for %s.%s", shell.ParentObjectName, shell.Name)
	}

	// setup the inputs and invoke it
	resolveParams := graphql.ResolveParams{
		Context: ctx,
		Source:  struct{}{}, // TODO: could support data fields too
		Args:    map[string]any{},
		Info: graphql.ResolveInfo{
			FieldName:  shell.Name,
			ParentType: s.MergedSchemas.Schema().Type(shell.ParentObjectName),
			// TODO: we don't currently use any of the other resolve info fields, but that could change
		},
	}
	for _, flag := range shell.Flags {
		resolveParams.Args[flag.Name] = flag.SetValue
	}
	res, err := fieldResolver(resolveParams)
	if err != nil {
		return "", fmt.Errorf("error resolving shell container: %w", err)
	}

	ctr, ok := res.(*core.Container)
	if !ok {
		return "", fmt.Errorf("unexpected result type %T from shell resolver", res)
	}

	// TODO: dedupe w/ containerSchema
	endpoint, handler, err := ctr.ShellEndpoint(s.bk, s.progSockPath)
	if err != nil {
		return "", fmt.Errorf("error getting shell endpoint: %w", err)
	}

	s.MuxEndpoint(path.Join("/", endpoint), handler)
	return "ws://dagger/" + endpoint, nil
}

// TODO:
var loadUniverseOnce = &sync.Once{}
var universeSchemas []ExecutableSchema
var loadUniverseErr error

func (s *environmentSchema) loadUniverse(ctx *core.Context, _ any, _ any) (any, error) {
	// TODO: unpacking to a tmpdir and loading as a local dir is dumb
	loadUniverseOnce.Do(func() {
		loadUniverseErr = func() error {
			tempdir, err := os.MkdirTemp("", "dagger-universe")
			if err != nil {
				return fmt.Errorf("failed to create tempdir: %w", err)
			}

			tarReader := tar.NewReader(bytes.NewReader(universe.Tar))
			var envPaths []string
			for {
				header, err := tarReader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("failed to read tar header: %w", err)
				}

				// TODO: hack to skip broken envs, remove
				if strings.HasPrefix(filepath.Clean(header.Name), "universe/_") {
					bklog.G(ctx).Warnf("SKIPPING ENV %s", header.Name)
					continue
				}

				if header.FileInfo().IsDir() {
					if err := os.MkdirAll(filepath.Join(tempdir, header.Name), header.FileInfo().Mode()); err != nil {
						return fmt.Errorf("failed to create dir %s: %w", header.Name, err)
					}
				} else {
					if filepath.Base(header.Name) == "dagger.json" && strings.HasPrefix(filepath.Clean(header.Name), "universe/") {
						envPaths = append(envPaths, filepath.Dir(header.Name))
					}

					if err := os.MkdirAll(filepath.Join(tempdir, filepath.Dir(header.Name)), header.FileInfo().Mode()); err != nil {
						return fmt.Errorf("failed to create dir %s: %w", filepath.Dir(header.Name), err)
					}
					f, err := os.OpenFile(filepath.Join(tempdir, header.Name), os.O_CREATE|os.O_WRONLY, header.FileInfo().Mode())
					if err != nil {
						return fmt.Errorf("failed to create file %s: %w", header.Name, err)
					}
					defer f.Close()
					if _, err := io.Copy(f, tarReader); err != nil {
						return fmt.Errorf("failed to copy file %s: %w", header.Name, err)
					}
				}
			}

			universeDir, err := core.NewHost().EngineServerDirectory(ctx, s.bk, tempdir, nil, "universe", s.platform, core.CopyFilter{})
			if err != nil {
				return fmt.Errorf("failed to load universe dir: %w", err)
			}

			universeSchemas = make([]ExecutableSchema, len(envPaths))
			var eg errgroup.Group
			for i, envPath := range envPaths {
				i, envPath := i, envPath
				eg.Go(func() error {
					// TODO: support dependencies
					envCfg, err := core.LoadEnvironmentConfig(ctx, s.bk, universeDir, envPath)
					if err != nil {
						return fmt.Errorf("failed to load environment config: %w", err)
					}

					// TODO: this doesn't work if the universe env was loaded before this call, but that's currently not possible
					// since load universe is hardcoded in the engine client to be called before Connect returns. Once that's fixed,
					// keep in mind
					_, err = s.loadedEnvCache.GetOrInitialize(envCfg.Name, func() (*core.Environment, error) {
						env, fieldResolver, err := core.LoadEnvironment(ctx, s.bk, s.progSockPath, universeDir.Pipeline, s.platform, universeDir, envPath)
						if err != nil {
							return nil, fmt.Errorf("failed to load environment: %w", err)
						}
						executableSchema, err := s.envToExecutableSchema(ctx, env, fieldResolver)
						if err != nil {
							return nil, fmt.Errorf("failed to convert environment to executable schema: %w", err)
						}
						universeSchemas[i] = executableSchema
						return env, nil
					})
					if err != nil {
						return fmt.Errorf("failed to load environment %s: %w", envCfg.Name, err)
					}
					return nil
				})
			}
			return eg.Wait()
		}()
	})
	if loadUniverseErr != nil {
		bklog.G(ctx).Fatalf("FAILED TO LOAD UNIVERSE: %s", loadUniverseErr)
		return nil, loadUniverseErr
	}

	err := s.addSchemas(universeSchemas...)
	if err != nil {
		return nil, fmt.Errorf("failed to add universe schemas: %w", err)
	}
	return true, nil
}

func (s *environmentSchema) functionID(ctx *core.Context, parent *core.EnvironmentFunction, args any) (core.EnvironmentFunctionID, error) {
	return parent.ID()
}

type withFunctionNameArgs struct {
	Name string
}

func (s *environmentSchema) withFunctionName(ctx *core.Context, parent *core.EnvironmentFunction, args withFunctionNameArgs) (*core.EnvironmentFunction, error) {
	return parent.WithName(args.Name), nil
}

type withFunctionDescriptionArgs struct {
	Description string
}

func (s *environmentSchema) withFunctionDescription(ctx *core.Context, parent *core.EnvironmentFunction, args withFunctionDescriptionArgs) (*core.EnvironmentFunction, error) {
	return parent.WithDescription(args.Description), nil
}

type withFunctionArgArgs struct {
	Name        string
	Description string
	ArgType     string
	IsList      bool
}

func (s *environmentSchema) withFunctionArg(ctx *core.Context, parent *core.EnvironmentFunction, args withFunctionArgArgs) (*core.EnvironmentFunction, error) {
	return parent.WithArg(core.EnvironmentFunctionArg{
		Name:        args.Name,
		Description: args.Description,
		ArgType:     args.ArgType,
		IsList:      args.IsList,
	}), nil
}

type withFunctionResultTypeArgs struct {
	Name string
}

func (s *environmentSchema) withFunctionResultType(ctx *core.Context, parent *core.EnvironmentFunction, args withFunctionResultTypeArgs) (*core.EnvironmentFunction, error) {
	return parent.WithResultType(args.Name), nil
}
