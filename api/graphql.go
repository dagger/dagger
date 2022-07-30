package api

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	tools "github.com/bhoriuchi/graphql-go-tools"
	"github.com/containerd/containerd/platforms"
	coreschema "github.com/dagger/cloak/api/schema"
	"github.com/dagger/cloak/tracing"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/printer"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	daggerSockName = "dagger-sock"
)

/*
	type AlpineBuild {
		fs: FS!
	}

	type Query {
		build(pkgs: [String]!): AlpineBuild
	}

converts to:

	type AlpineBuild {
		fs: FS!
	}

	type Alpine {
		build(pkgs: [String]!): AlpineBuild
	}

	type Query {
		alpine: Alpine!
	}
*/
func parseSchema(pkgName string, typeDefs string) (*tools.ExecutableSchema, error) {
	capName := strings.ToUpper(string(pkgName[0])) + pkgName[1:]
	resolverMap := tools.ResolverMap{
		"Query": &tools.ObjectResolver{
			Fields: tools.FieldResolveMap{
				pkgName: &tools.FieldResolve{
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return struct{}{}, nil
					},
				},
			},
		},
		capName: &tools.ObjectResolver{
			Fields: tools.FieldResolveMap{},
		},
	}

	doc, err := parser.Parse(parser.ParseParams{Source: typeDefs})
	if err != nil {
		return nil, err
	}

	var actions []string
	var otherObjects []string
	var actionDoc string
	for _, def := range doc.Definitions {
		if def, ok := def.(*ast.ObjectDefinition); ok {
			if def.Name.Value == "Query" {
				if def.Description != nil {
					actionDoc = def.Description.Value
				}
				for _, field := range def.Fields {
					actions = append(actions, printer.Print(field).(string))
					resolverMap[capName].(*tools.ObjectResolver).Fields[field.Name.Value] = &tools.FieldResolve{
						Resolve: actionFieldToResolver(pkgName, field.Name.Value),
					}
				}
			} else {
				otherObjects = append(otherObjects, printer.Print(def).(string))
			}
		}
	}

	return &tools.ExecutableSchema{
		TypeDefs: fmt.Sprintf(`
%s

%q
type %s {
	%s
}

type Query {
	%q
	%s: %s!
}
	`,
			strings.Join(otherObjects, "\n"),
			actionDoc,
			capName,
			strings.Join(actions, "\n"),
			actionDoc,
			pkgName,
			capName,
		),
		Resolvers: resolverMap,
	}, nil
}

func actionFieldToResolver(pkgName, actionName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		pathArray := p.Info.Path.AsArray()
		lastPath := pathArray[len(pathArray)-1]
		inputMap := map[string]interface{}{
			"object": lastPath.(string),
			"args":   p.Args,
		}
		inputBytes, err := json.Marshal(inputMap)
		if err != nil {
			return nil, err
		}

		// TODO: inputs should also include the versions of all deps so that if a dep changes, this runs again too
		input := llb.Scratch().File(llb.Mkfile("/dagger.json", 0644, inputBytes))

		fsState, err := daggerPackages[pkgName].FS.ToState()
		if err != nil {
			return nil, err
		}
		st := fsState.Run(
			llb.Args([]string{"/entrypoint"}),
			llb.AddSSHSocket(
				llb.SSHID(daggerSockName),
				llb.SSHSocketTarget("/dagger.sock"),
			),
			llb.AddMount("/inputs", input, llb.Readonly),
			llb.AddMount("/tmp", llb.Scratch(), llb.Tmpfs()),
			llb.ReadonlyRootFS(),
		)

		// TODO: /mnt should maybe be configurable?
		for path, fsid := range collectFSPaths(p.Args, "/mnt", make(map[string]FSID)) {
			fsState, err := (&Filesystem{ID: fsid}).ToState()
			if err != nil {
				return nil, err
			}
			// TODO: it should be possible for this to be outputtable by the action; the only question
			// is how to expose that ability in a non-confusing way, just needs more thought
			st.AddMount(path, fsState, llb.ForceNoOutput)
		}

		outputMnt := st.AddMount("/outputs", llb.Scratch())
		outputDef, err := outputMnt.Marshal(p.Context, llb.Platform(getPlatform(p)), llb.WithCustomName(fmt.Sprintf("%s.%s", pkgName, actionName)))
		if err != nil {
			return nil, err
		}

		gw, err := getGatewayClient(p)
		if err != nil {
			return nil, err
		}
		res, err := gw.Solve(context.Background(), bkgw.SolveRequest{
			Evaluate:   true,
			Definition: outputDef.ToPB(),
		})
		if err != nil {
			return nil, err
		}
		ref, err := res.SingleRef()
		if err != nil {
			return nil, err
		}
		outputBytes, err := ref.ReadFile(p.Context, bkgw.ReadRequest{
			Filename: "/dagger.json",
		})
		if err != nil {
			return nil, err
		}
		// fmt.Printf("%s.%s output: %s\n", pkgName, actionName, string(outputBytes))
		var output interface{}
		if err := json.Unmarshal(outputBytes, &output); err != nil {
			return nil, fmt.Errorf("failed to unmarshal output: %w", err)
		}
		return output, nil
	}
}

func collectFSPaths(arg interface{}, curPath string, fsPaths map[string]FSID) map[string]FSID {
	switch arg := arg.(type) {
	case FSID:
		// TODO: make sure there can't be any shenanigans with args named e.g. ../../../foo/bar
		fsPaths[curPath] = arg
	case map[string]interface{}:
		for k, v := range arg {
			fsPaths = collectFSPaths(v, filepath.Join(curPath, k), fsPaths)
		}
	case []interface{}:
		for i, v := range arg {
			// TODO: path format technically works but weird as hell, gotta be a better way
			fsPaths = collectFSPaths(v, fmt.Sprintf("%s/%d", curPath, i), fsPaths)
		}
	}
	return fsPaths
}

type daggerPackage struct {
	Name   string
	FS     *Filesystem
	Schema tools.ExecutableSchema
}

// TODO: shouldn't be global vars, pass through resolve context, make sure synchronization is handled, etc.
var schemaLock sync.RWMutex
var schema graphql.Schema
var daggerPackages map[string]daggerPackage

func reloadSchemas() error {
	// tools.MakeExecutableSchema doesn't actually merge multiple schemas, it just overwrites any
	// overlapping types This is fine for the moment except for the Query type, which we need to be an
	// actual merge, so we do that ourselves here by merging the Query resolvers and appending a merged
	// Query type to the typeDefs.
	var queryFields []string
	var otherObjects []string
	for _, daggerPkg := range daggerPackages {
		doc, err := parser.Parse(parser.ParseParams{Source: daggerPkg.Schema.TypeDefs})
		if err != nil {
			return err
		}
		for _, def := range doc.Definitions {
			if def, ok := def.(*ast.ObjectDefinition); ok {
				if def.Name.Value == "Query" {
					for _, field := range def.Fields {
						queryFields = append(queryFields, printer.Print(field).(string))
					}
					continue
				}
			}
			otherObjects = append(otherObjects, printer.Print(def).(string))
		}
	}

	resolvers := make(map[string]interface{})
	for _, daggerPkg := range daggerPackages {
		for k, v := range daggerPkg.Schema.Resolvers {
			// TODO: need more general solution, verification that overwrites aren't happening, etc.
			if k == "Query" {
				if existing, ok := resolvers[k]; ok {
					existing := existing.(*tools.ObjectResolver)
					for field, fieldResolver := range v.(*tools.ObjectResolver).Fields {
						existing.Fields[field] = fieldResolver
					}
					v = existing
				}
			}
			resolvers[k] = v
		}
	}

	typeDefs := fmt.Sprintf(`
%s
type Query {
  %s
}
	`, strings.Join(otherObjects, "\n"), strings.Join(queryFields, "\n "))

	var err error
	schema, err = tools.MakeExecutableSchema(tools.ExecutableSchema{
		TypeDefs:   typeDefs,
		Resolvers:  resolvers,
		Extensions: []graphql.Extension{&tracing.GraphQLTracer{}},
	})
	if err != nil {
		return err
	}

	return nil
}

func init() {
	daggerPackages = make(map[string]daggerPackage)
	daggerPackages["core"] = daggerPackage{
		Name: "core",
		Schema: tools.ExecutableSchema{
			TypeDefs: coreschema.Schema,
			Resolvers: tools.ResolverMap{
				"Query": &tools.ObjectResolver{
					Fields: tools.FieldResolveMap{
						"core": &tools.FieldResolve{
							Resolve: func(p graphql.ResolveParams) (interface{}, error) {
								return struct{}{}, nil
							},
						},
					},
				},

				// FIXME: chaining experiment
				"Core":       coreResolver,
				"Filesystem": filesystemResolver,
				"Exec":       execResolver,

				"Mutation": &tools.ObjectResolver{
					Fields: tools.FieldResolveMap{
						"import": &tools.FieldResolve{
							Resolve: func(p graphql.ResolveParams) (interface{}, error) {
								schemaLock.Lock()
								defer schemaLock.Unlock()
								// TODO: make sure not duped
								pkgName, ok := p.Args["name"].(string)
								if !ok {
									return nil, fmt.Errorf("invalid package name")
								}

								if pkgName == "core" {
									return map[string]interface{}{
										"name":       pkgName,
										"schema":     daggerPackages["core"].Schema.TypeDefs.(string),
										"operations": coreschema.Operations,
									}, nil
								}

								fsid, ok := p.Args["fs"].(FSID)
								if !ok {
									return nil, fmt.Errorf("invalid fs")
								}
								fs := &Filesystem{ID: fsid}
								pbdef, err := fs.ToDefinition()
								if err != nil {
									return nil, err
								}

								gw, err := getGatewayClient(p)
								if err != nil {
									return nil, err
								}
								res, err := gw.Solve(context.Background(), bkgw.SolveRequest{
									Evaluate:   true,
									Definition: pbdef,
								})
								if err != nil {
									return nil, err
								}
								bkref, err := res.SingleRef()
								if err != nil {
									return nil, err
								}
								schemaBytes, err := bkref.ReadFile(p.Context, bkgw.ReadRequest{
									Filename: "/schema.graphql",
								})
								if err != nil {
									return nil, err
								}
								parsed, err := parseSchema(pkgName, string(schemaBytes))
								if err != nil {
									return nil, err
								}
								daggerPackages[pkgName] = daggerPackage{
									Name:   pkgName,
									FS:     fs,
									Schema: *parsed,
								}
								operationsBytes, err := bkref.ReadFile(p.Context, bkgw.ReadRequest{
									Filename: "/operations.graphql",
								})
								if err != nil {
									return nil, err
								}

								if err := reloadSchemas(); err != nil {
									return nil, err
								}

								parsedSchema := parsed.TypeDefs.(string)

								return map[string]interface{}{
									"name":       pkgName,
									"fs":         fs,
									"schema":     parsedSchema,
									"operations": string(operationsBytes),
								}, nil
							},
						},
					},
				},
				"SecretID": &tools.ScalarResolver{
					Serialize: func(value interface{}) interface{} {
						switch v := value.(type) {
						case string:
							return v
						default:
							panic(fmt.Sprintf("unexpected secret type %T", v))
						}
					},
					ParseValue: func(value interface{}) interface{} {
						switch v := value.(type) {
						case string:
							return v
						default:
							panic(fmt.Sprintf("unexpected secret value type %T: %+v", v, v))
						}
					},
					ParseLiteral: func(valueAST ast.Value) interface{} {
						switch valueAST := valueAST.(type) {
						case *ast.StringValue:
							return valueAST.Value
						default:
							panic(fmt.Sprintf("unexpected secret literal type: %T", valueAST))
						}
					},
				},
				"FSID": &tools.ScalarResolver{
					Serialize: func(value interface{}) interface{} {
						switch v := value.(type) {
						case FSID, string:
							return v
						default:
							panic(fmt.Sprintf("unexpected fsid type %T", v))
						}
					},
					ParseValue: func(value interface{}) interface{} {
						switch v := value.(type) {
						case string:
							return FSID(v)
						default:
							panic(fmt.Sprintf("unexpected fsid value type %T: %+v", v, v))
						}
					},
					ParseLiteral: func(valueAST ast.Value) interface{} {
						switch valueAST := valueAST.(type) {
						case *ast.StringValue:
							return FSID(valueAST.Value)
						default:
							panic(fmt.Sprintf("unexpected fsid literal type: %T", valueAST))
						}
					},
				},
			},
		},
	}

	if err := reloadSchemas(); err != nil {
		panic(err)
	}
}

type gatewayClientKey struct{}

func withGatewayClient(ctx context.Context, gw bkgw.Client) context.Context {
	return context.WithValue(ctx, gatewayClientKey{}, gw)
}

func getGatewayClient(p graphql.ResolveParams) (bkgw.Client, error) {
	v := p.Context.Value(gatewayClientKey{})
	if v == nil {
		return nil, fmt.Errorf("no gateway client")
	}
	return v.(bkgw.Client), nil
}

type platformKey struct{}

func withPlatform(ctx context.Context, platform *specs.Platform) context.Context {
	return context.WithValue(ctx, platformKey{}, platform)
}

func getPlatform(p graphql.ResolveParams) specs.Platform {
	v := p.Context.Value(platformKey{})
	if v == nil {
		return platforms.DefaultSpec()
	}
	return *v.(*specs.Platform)
}

type secretsKey struct{}

func withSecrets(ctx context.Context, secrets map[string]string) context.Context {
	return context.WithValue(ctx, secretsKey{}, secrets)
}

func getSecrets(p graphql.ResolveParams) map[string]string {
	v := p.Context.Value(secretsKey{})
	if v == nil {
		return nil
	}
	return v.(map[string]string)
}

func getQuery(p graphql.ResolveParams) string {
	return printer.Print(p.Info.Operation).(string)
}

func getOperationName(p graphql.ResolveParams) string {
	name := p.Info.Operation.(*ast.OperationDefinition).Name
	if name == nil {
		return ""
	}
	return name.Value
}
