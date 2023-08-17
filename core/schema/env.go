package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/graphql"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
)

type environmentSchema struct {
	*MergedSchemas
	loadedEnvCache   *core.CacheMap[string, *core.Environment]        // env name -> env
	checkResultCache *core.CacheMap[digest.Digest, *core.CheckResult] // env name -> env
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
			"currentEnvironment": ToResolver(s.currentEnvironment),
		},
		"Environment": ToIDableObjectResolver(core.EnvironmentID.ToEnvironment, ObjectResolver{
			"id":        ToResolver(s.environmentID),
			"load":      ToResolver(s.load),
			"name":      ToResolver(s.environmentName),
			"withCheck": ToResolver(s.withCheck),
		}),
		"Check": ToIDableObjectResolver(core.CheckID.ToCheck, ObjectResolver{
			"id":              ToResolver(s.checkID),
			"withName":        ToResolver(s.withCheckName),
			"withDescription": ToResolver(s.withCheckDescription),
			"withSubcheck":    ToResolver(s.withSubcheck),
			"withContainer":   ToResolver(s.withCheckContainer),
			"result":          ToResolver(s.checkResult),
		}),
		"CheckResult": ToIDableObjectResolver(core.CheckID.ToCheck, ObjectResolver{
			"id":          ToResolver(s.resultID),
			"withName":    ToResolver(s.withResultName),
			"withSuccess": ToResolver(s.withResultSuccess),
			"withOutput":  ToResolver(s.withResultOutput),
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
		env, err := core.LoadEnvironment(ctx, s.bk, s.progSockPath, rootDir.Pipeline, s.platform, rootDir, args.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load environment: %w", err)
		}

		executableSchema, err := s.envToExecutableSchema(ctx, env, rootDir.Pipeline)
		if err != nil {
			return nil, fmt.Errorf("failed to convert environment to executable schema: %w", err)
		}
		if err := s.addSchemas(executableSchema); err != nil {
			return nil, fmt.Errorf("failed to install environment schema: %w", err)
		}
		return env, nil
	})
}

func (s *environmentSchema) envToExecutableSchema(ctx *core.Context, env *core.Environment, pipeline pipeline.Path) (ExecutableSchema, error) {
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
		// TODO: ugly spaghetti, for checks the resolver in the environment is for just the result field, not
		// the whole check object. That's fine but need some less convoluted code implementing this pattern
		if field.Type.Name() == "Check" {
			var check *core.Check
			for _, candidateCheck := range env.Checks {
				if candidateCheck.Name == field.Name {
					check = candidateCheck
					break
				}
			}
			if check != nil { // could just be a Function
				// TODO:
				bklog.G(ctx).Debugf("ADDING RESOLVER FOR CHECK %s", field.Name)

				objResolver[field.Name] = ToResolver(func(ctx *core.Context, parent any, args any) (any, error) {
					argBytes, err := json.Marshal(args)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal args: %w", err)
					}
					argMap := map[string]any{}
					if err := json.Unmarshal(argBytes, &argMap); err != nil {
						return nil, fmt.Errorf("failed to unmarshal args: %w", err)
					}
					for argName, argVal := range argMap {
						argValStr, ok := argVal.(string)
						if !ok {
							return nil, fmt.Errorf("expected check arg %s to be a string, got %T", argName, argVal)
						}
						check, err = check.SetStringFlag(argName, argValStr)
						if err != nil {
							return nil, fmt.Errorf("failed to set check arg %s: %w", argName, err)
						}
					}
					// TODO:
					bklog.G(ctx).Debugf("CHECK RESOLVER %s %s %+v", field.Name, ctx.ResolveParams.Info.Path.AsArray(), check)
					return check, nil
				})
				continue
			}
		}

		fieldResolver, err := env.FieldResolver(ctx, s.bk, s.progSockPath, pipeline)
		if err != nil {
			return nil, fmt.Errorf("failed to get field resolver for %s: %w", field.Name, err)
		}
		objResolver[field.Name] = ToResolver(func(ctx *core.Context, parent any, args any) (any, error) {
			res, err := fieldResolver(ctx, parent, args)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve field %s: %w", field.Name, err)
			}
			res, err = convertOutput(res, field.Type, s.MergedSchemas, env)
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

func convertOutput(rawOutput any, schemaOutputType *ast.Type, s *MergedSchemas, env *core.Environment) (any, error) {
	if schemaOutputType.Elem != nil {
		schemaOutputType = schemaOutputType.Elem
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
		// between the server and environment container.
		outputStr, ok := rawOutput.(string)
		if !ok {
			return nil, fmt.Errorf("expected id string output for %s, got %T", objectName, rawOutput)
		}
		obj, err := resolver.FromID(outputStr)
		if err != nil {
			return nil, fmt.Errorf("failed to convert output to %s: %w", objectName, err)
		}
		return obj, nil
	}
	return rawOutput, nil
}

type withCheckArgs struct {
	ID core.CheckID
}

func (s *environmentSchema) withCheck(ctx *core.Context, parent *core.Environment, args withCheckArgs) (*core.Environment, error) {
	cmd, err := args.ID.ToCheck()
	if err != nil {
		return nil, err
	}
	return parent.WithCheck(ctx, cmd)
}

type checkArgs struct {
	ID core.CheckID
}

func (s *environmentSchema) check(ctx *core.Context, parent *core.Query, args checkArgs) (*core.Check, error) {
	return core.NewCheck(args.ID)
}

type checkResultArgs struct {
	Success bool
	Output  string
}

func (s *environmentSchema) checkResult(ctx *core.Context, parent *core.Query, args checkResultArgs) (*core.CheckResult, error) {
	return &core.CheckResult{
		Success: args.Success,
		Output:  args.Output,
	}, nil
}

func (s *environmentSchema) currentEnvironment(ctx *core.Context, parent *core.Query, args any) (*core.Environment, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.loadedEnvCache.GetOrInitialize(clientMetadata.EnvironmentName, func() (*core.Environment, error) {
		return nil, fmt.Errorf("no such environment %s", clientMetadata.EnvironmentName)
	})
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
	return parent.WithSubcheck(args.ID), nil
}

type withCheckContainerArgs struct {
	ID core.ContainerID
}

func (s *environmentSchema) withCheckContainer(ctx *core.Context, parent *core.Check, args withCheckContainerArgs) (*core.Check, error) {
	return parent.WithContainer(args.ID), nil
}

func (s *environmentSchema) checkResult(ctx *core.Context, check *core.Check, _ any) (rres *core.CheckResult, rerr error) {
	dig, err := check.Digest()
	if err != nil {
		return nil, err
	}

	// TODO(vito): more evidence of the need for a global query cache. we want to
	// relay output to the Progrock vertex, but this resolver is called multiple
	// times, leading to duplicate output
	return s.checkResultCache.GetOrInitialize(dig, func() (*core.CheckResult, error) {
		return s.checkResultInner(ctx, dig, check)
	})
}

func (s *environmentSchema) checkResultInner(ctx *core.Context, dig digest.Digest, check *core.Check) (rres *core.CheckResult, rerr error) {
	recorder := ctx.Recorder()

	name := check.Name
	if name == "" {
		name = check.Description
	}

	vtxName := name
	if vtxName == "" {
		// TODO
		vtxName = fmt.Sprintf("unnamed check: %s", dig)
	}

	if check.EnvironmentName != "" {
		recorder = recorder.WithGroup(check.EnvironmentName,
			progrock.WithGroupID(dig.String()+":"+check.EnvironmentName))
	}

	if len(check.Subchecks) == 0 {
		vtx := recorder.Vertex(dig+":result", vtxName, progrock.Focused())

		defer func() {
			if rerr != nil {
				fmt.Fprintln(vtx.Stderr(), rerr.Error())
				vtx.Done(rerr)
			} else if rres != nil {
				fmt.Fprint(vtx.Stdout(), rres.Output)
				if rres.Success {
					vtx.Complete()
				} else {
					vtx.Done(fmt.Errorf("failed"))
				}
			}
		}()

		ctx.Vertex = vtx // NB: nothing uses this atm, just seems appropriate
	}

	if name != "" {
		// initialize subgroup _after_ vertex above so that it only shows up if any
		// further vertices are sent (e.g. Container eval)
		recorder = recorder.WithGroup(name, progrock.WithGroupID(dig.String()))
		ctx = ctx.WithRecorder(recorder)
	}

	// if there's no subchecks, resolve the result directly
	if len(check.Subchecks) == 0 {
		// TODO:
		bklog.G(ctx).Debugf("CHECK RESULT RESOLVER %s %+v %+v", check.Name, ctx.ResolveParams.Info.Path.AsArray(), check)

		if check.ContainerID != "" {
			ctr, err := check.ContainerID.ToContainer()
			if err != nil {
				return nil, err
			}
			output, err := ctr.Stdout(ctx, s.bk, s.progSockPath) // TODO(vito): combined output
			if err != nil {
				return &core.CheckResult{
					Name:    check.EnvironmentName + "." + check.Name,
					Success: false,
					Output:  err.Error(),
				}, nil
			}
			return &core.CheckResult{
				Name:    check.EnvironmentName + "." + check.Name,
				Success: true,
				Output:  output,
			}, nil
		}

		// resolve the result directly
		// TODO: more strands of spaghetti
		env, err := s.loadedEnvCache.GetOrInitialize(check.EnvironmentName, func() (*core.Environment, error) {
			return nil, fmt.Errorf("environment %s not found", check.EnvironmentName)
		})
		if err != nil {
			return nil, err
		}
		fieldResolver, err := env.FieldResolver(ctx, s.bk, s.progSockPath, nil) // TODO: set pipline to something
		if err != nil {
			return nil, err
		}
		envObjName := strings.ToUpper(env.Config.Name[:1]) + env.Config.Name[1:]
		resolveParams := graphql.ResolveParams{
			Context: ctx,
			Source:  struct{}{},
			Args:    map[string]any{},
			Info: graphql.ResolveInfo{
				FieldName:  check.Name,
				ParentType: s.MergedSchemas.Schema().Type(envObjName),
			},
		}
		for _, flag := range check.Flags {
			resolveParams.Args[flag.Name] = flag.SetValue
		}
		// TODO: hack to invalidate cache so that we always run the resolver,
		// actual fix should be to change progress printing for results such that
		// it's printed recursively and only once
		resolveParams.Args["_cachebust"] = identity.NewID()

		res, err := fieldResolver(&core.Context{
			Context:       ctx,
			ResolveParams: resolveParams,
			Vertex:        ctx.Vertex,
		}, resolveParams.Source, resolveParams.Args)
		if err != nil {
			return nil, fmt.Errorf("error resolving check %s.%s: %w", check.EnvironmentName, check.Name, err)
		}

		// TODO: ugly

		var checkRes core.CheckResult
		switch v := res.(type) {
		case string:
			if subCheck, err := core.CheckID(v).ToCheck(); err == nil {
				// NB: run with ctx that places us in the current group!
				res, err := s.checkResult(ctx, subCheck, nil)
				if err != nil {
					return nil, fmt.Errorf("get result of returned check: %w", err)
				}

				checkRes = *res
			} else if res, err := core.CheckResultID(v).ToCheckResult(); err == nil {
				checkRes = *res
			} else {
				// if the sdk returned a regular string, that means success and the
				// output is the string
				checkRes.Success = true
				checkRes.Output = v
			}
		default:
			return nil, fmt.Errorf("check %s.%s returned unexpected type %T", check.EnvironmentName, check.Name, res)
		}

		checkRes.Name = check.EnvironmentName + "." + check.Name

		// TODO:
		bklog.G(ctx).Debugf("CHECK RESULT RESOLVER RETURNED %s %+v %+v", check.Name, res, checkRes)
		return &checkRes, nil
	}

	// otherwise, resolve each subcheck and construct the result from that

	// TODO:
	bklog.G(ctx).Debugf("CHECK SUBRESULT RESOLVER %s %+v", check.Name, check)

	// TODO: guard against infinite recursion
	checkRes := &core.CheckResult{
		Name:       check.EnvironmentName + "." + check.Name,
		Subresults: make([]*core.CheckResult, len(check.Subchecks)),
		Success:    true,
		// TODO: could combine output in theory, but not sure what the format would be.
		// For now, output can just be collected from subresults
	}
	var eg errgroup.Group
	for i, subcheckID := range check.Subchecks {
		i, subcheckID := i, subcheckID
		eg.Go(func() error {
			subcheck, err := subcheckID.ToCheck()
			if err != nil {
				return err
			}
			// subDig, err := subcheck.Digest()
			// if err != nil {
			// 	return err
			// }
			// vtx.Output(subDig)
			subresult, err := s.checkResult(ctx, subcheck, nil)
			if err != nil {
				return err
			}
			checkRes.Subresults[i] = subresult
			if !subresult.Success {
				checkRes.Success = false
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return checkRes, nil
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

func (s *environmentSchema) withResultSuccess(
	ctx *core.Context,
	result *core.CheckResult,
	args struct{ Success bool },
) (*core.CheckResult, error) {
	result = result.Clone()
	result.Success = args.Success
	return result, nil
}

func (s *environmentSchema) withResultOutput(
	ctx *core.Context,
	result *core.CheckResult,
	args struct{ Output string },
) (*core.CheckResult, error) {
	result = result.Clone()
	result.Output = args.Output
	return result, nil
}

func (s *environmentSchema) withResultSubresult(
	ctx *core.Context,
	result *core.CheckResult,
	args struct{ Result core.CheckResultID },
) (*core.CheckResult, error) {
	result = result.Clone()
	res, err := args.Result.ToCheckResult()
	if err != nil {
		return nil, err
	}
	result.Subresults = append(result.Subresults, res)
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
