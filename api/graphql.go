package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	tools "github.com/bhoriuchi/graphql-go-tools"
	"github.com/containerd/containerd/platforms"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/printer"
	"github.com/moby/buildkit/client/llb"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	daggerSockName = "dagger-sock"
)

// Mirrors handler.RequestOptions, but includes omitempty for better compatibility
// with other servers like apollo (which don't seem to like "operationName": "").
type GraphQLRequest struct {
	Query         string                 `json:"query,omitempty"`
	Variables     map[string]interface{} `json:"variables,omitempty"`
	OperationName string                 `json:"operationName,omitempty"`
}

// FS is either llb representing the filesystem or a graphql query for obtaining that llb
// This is opaque to clients; to them FS is a scalar.
type FS struct {
	PB *pb.Definition
	GraphQLRequest
}

// FS encodes to the base64 encoding of its JSON representation
func (fs FS) MarshalText() ([]byte, error) {
	type marshalFS FS
	jsonBytes, err := json.Marshal(marshalFS(fs))
	if err != nil {
		return nil, err
	}
	b64Bytes := make([]byte, base64.StdEncoding.EncodedLen(len(jsonBytes)))
	base64.StdEncoding.Encode(b64Bytes, jsonBytes)
	return b64Bytes, nil
}

func (fs *FS) UnmarshalText(b64Bytes []byte) error {
	type marshalFS FS
	jsonBytes := make([]byte, base64.StdEncoding.DecodedLen(len(b64Bytes)))
	n, err := base64.StdEncoding.Decode(jsonBytes, b64Bytes)
	if err != nil {
		return fmt.Errorf("failed to unmarshal fs bytes: %v", err)
	}
	jsonBytes = jsonBytes[:n]
	var result marshalFS
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return fmt.Errorf("failed to unmarshal result: %v: %s", err, string(jsonBytes))
	}
	fs.PB = result.PB
	fs.GraphQLRequest = result.GraphQLRequest
	return nil
}

func (fs FS) ToState() (llb.State, error) {
	if fs.PB == nil {
		return llb.State{}, fmt.Errorf("FS is not evaluated")
	}
	defop, err := llb.NewDefinitionOp(fs.PB)
	if err != nil {
		return llb.State{}, err
	}
	return llb.NewState(defop), nil
}

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
	for _, def := range doc.Definitions {
		if def, ok := def.(*ast.ObjectDefinition); ok {
			if def.Name.Value == "Query" {
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
type %s {
	%s
}
type Query {
	%s: %s!
}
	`, strings.Join(otherObjects, "\n"), capName, strings.Join(actions, "\n"), pkgName, capName),
		Resolvers: resolverMap,
	}, nil
}

func actionFieldToResolver(pkgName, actionName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		if !shouldEval(p.Context) {
			return lazyResolve(p)
		}

		// the action doesn't know we stitch its queries under the package name, patch the query we send to here
		queryOp := p.Info.Operation.(*ast.OperationDefinition)
		packageSelect := queryOp.SelectionSet.Selections[0].(*ast.Field)
		queryOp.SelectionSet.Selections = packageSelect.SelectionSet.Selections

		inputBytes, err := json.Marshal(GraphQLRequest{
			Query:         printer.Print(queryOp).(string),
			Variables:     p.Info.VariableValues,
			OperationName: getOperationName(p),
		})
		if err != nil {
			return nil, err
		}
		// fmt.Printf("requesting %s\n", string(inputBytes))

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
		for path, fs := range collectFSPaths(p.Args, "/mnt", make(map[string]FS)) {
			fsState, err := fs.ToState()
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
		for _, parentField := range append([]any{"data"}, p.Info.Path.AsArray()[1:]...) { // skip first field, which is the package name
			outputMap, ok := output.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("output is not a map: %+v", output)
			}
			output = outputMap[parentField.(string)]
		}
		return output, nil
	}
}

func collectFSPaths(arg interface{}, curPath string, fsPaths map[string]FS) map[string]FS {
	switch arg := arg.(type) {
	case FS:
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
	FS     FS
	Schema tools.ExecutableSchema
}

// TODO: shouldn't be global vars, pass through resolve context, make sure synchronization is handled, etc.
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
		TypeDefs:  typeDefs,
		Resolvers: resolvers,
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
			TypeDefs: coreSchema,
			Resolvers: tools.ResolverMap{
				"Query": &tools.ObjectResolver{
					Fields: tools.FieldResolveMap{
						"core": &tools.FieldResolve{
							Resolve: func(p graphql.ResolveParams) (interface{}, error) {
								return struct{}{}, nil
							},
						},
						"source": &tools.FieldResolve{
							Resolve: func(p graphql.ResolveParams) (interface{}, error) {
								return struct{}{}, nil
							},
						},
					},
				},

				// FIXME: chaining experiment
				"Source":     sourceResolver,
				"Filesystem": filesystemResolver,
				"Exec":       execResolver,

				"CoreExec": &tools.ObjectResolver{
					Fields: tools.FieldResolveMap{
						"getMount": &tools.FieldResolve{
							Resolve: func(p graphql.ResolveParams) (interface{}, error) {
								parent, ok := p.Source.(map[string]interface{})
								if !ok {
									return nil, fmt.Errorf("unexpected core exec parent type %T", p.Source)
								}
								mounts, ok := parent["mounts"].(map[string]FS)
								if !ok {
									return nil, fmt.Errorf("unexpected core exec mounts type %T", parent["mounts"])
								}
								path, ok := p.Args["path"].(string)
								if !ok {
									return nil, fmt.Errorf("invalid path argument")
								}
								fs, ok := mounts[path]
								if !ok {
									return nil, fmt.Errorf("mount at path %q not found", path)
								}
								return fs, nil
							},
						},
					},
				},
				"Core": &tools.ObjectResolver{
					Fields: tools.FieldResolveMap{
						"image": &tools.FieldResolve{
							Resolve: func(p graphql.ResolveParams) (interface{}, error) {
								if !shouldEval(p.Context) {
									return lazyResolve(p)
								}
								rawRef, ok := p.Args["ref"]
								if !ok {
									return nil, fmt.Errorf("missing ref")
								}
								ref, ok := rawRef.(string)
								if !ok {
									return nil, fmt.Errorf("ref is not a string")
								}
								llbdef, err := llb.Image(ref).Marshal(p.Context, llb.Platform(getPlatform(p)))
								if err != nil {
									return nil, err
								}
								gw, err := getGatewayClient(p)
								if err != nil {
									return nil, err
								}
								_, err = gw.Solve(context.Background(), bkgw.SolveRequest{
									Evaluate:   true,
									Definition: llbdef.ToPB(),
								})
								if err != nil {
									return nil, err
								}
								return map[string]interface{}{
									"fs": FS{PB: llbdef.ToPB()},
								}, nil
							},
						},
						"exec": &tools.FieldResolve{
							Resolve: func(p graphql.ResolveParams) (interface{}, error) {
								if !shouldEval(p.Context) {
									return lazyResolve(p)
								}
								input, ok := p.Args["input"].(map[string]interface{})
								if !ok {
									return nil, fmt.Errorf("invalid exec input: %+v", p.Args["input"])
								}

								workdir, ok := input["workdir"].(string)
								if !ok {
									workdir = ""
								}

								rawArgs, ok := input["args"].([]interface{})
								if !ok {
									return nil, fmt.Errorf("invalid args")
								}
								var args []string
								for _, arg := range rawArgs {
									if arg, ok := arg.(string); ok {
										args = append(args, arg)
									} else {
										return nil, fmt.Errorf("invalid arg")
									}
								}

								rawMounts, ok := input["mounts"].([]interface{})
								if !ok {
									return nil, fmt.Errorf("invalid mounts")
								}
								mounts := map[string]FS{}
								for _, rawMount := range rawMounts {
									mount, ok := rawMount.(map[string]interface{})
									if !ok {
										return nil, fmt.Errorf("invalid mount")
									}
									path, ok := mount["path"].(string)
									if !ok {
										return nil, fmt.Errorf("invalid mount path")
									}
									fs, ok := mount["fs"].(FS)
									if !ok {
										return nil, fmt.Errorf("invalid mount fs")
									}
									mounts[path] = fs
								}
								root, ok := mounts["/"]
								if !ok {
									return nil, fmt.Errorf("missing root mount")
								}
								rootState, err := root.ToState()
								if err != nil {
									return nil, err
								}
								execState := rootState.Run(llb.Args(args), llb.Dir(workdir))
								gw, err := getGatewayClient(p)
								if err != nil {
									return nil, err
								}
								for path, mount := range mounts {
									if path == "/" {
										continue
									}
									state, err := mount.ToState()
									if err != nil {
										return nil, err
									}
									state = execState.AddMount(path, state)
									llbdef, err := state.Marshal(p.Context, llb.Platform(getPlatform(p)))
									if err != nil {
										return nil, err
									}
									_, err = gw.Solve(context.Background(), bkgw.SolveRequest{
										Evaluate:   true,
										Definition: llbdef.ToPB(),
									})
									if err != nil {
										return nil, err
									}
									mounts[path] = FS{PB: llbdef.ToPB()}
								}
								llbdef, err := execState.Root().Marshal(p.Context, llb.Platform(getPlatform(p)))
								if err != nil {
									return nil, err
								}
								_, err = gw.Solve(context.Background(), bkgw.SolveRequest{
									Evaluate:   true,
									Definition: llbdef.ToPB(),
								})
								if err != nil {
									return nil, err
								}
								return map[string]interface{}{
									"root":   FS{PB: llbdef.ToPB()},
									"mounts": mounts,
								}, nil
							},
						},
						"dockerfile": &tools.FieldResolve{
							Resolve: func(p graphql.ResolveParams) (interface{}, error) {
								if !shouldEval(p.Context) {
									return lazyResolve(p)
								}

								fs, ok := p.Args["context"].(FS)
								if !ok {
									return nil, fmt.Errorf("invalid context")
								}

								var dockerfileName string
								rawDockerfileName, ok := p.Args["dockerfileName"]
								if ok {
									dockerfileName, ok = rawDockerfileName.(string)
									if !ok {
										return nil, fmt.Errorf("invalid dockerfile name: %+v", rawDockerfileName)
									}
								}

								gw, err := getGatewayClient(p)
								if err != nil {
									return nil, err
								}

								opts := map[string]string{
									"platform": platforms.Format(getPlatform(p)),
								}
								inputs := map[string]*pb.Definition{
									dockerfilebuilder.DefaultLocalNameContext:    fs.PB,
									dockerfilebuilder.DefaultLocalNameDockerfile: fs.PB,
								}
								if dockerfileName != "" {
									opts["filename"] = dockerfileName
								}
								res, err := gw.Solve(p.Context, bkgw.SolveRequest{
									Frontend:       "dockerfile.v0",
									FrontendOpt:    opts,
									FrontendInputs: inputs,
								})
								if err != nil {
									return nil, err
								}

								bkref, err := res.SingleRef()
								if err != nil {
									return nil, err
								}
								st, err := bkref.ToState()
								if err != nil {
									return nil, err
								}
								llbdef, err := st.Marshal(p.Context, llb.Platform(getPlatform(p)))
								if err != nil {
									return nil, err
								}
								return FS{PB: llbdef.ToPB()}, nil
							},
						},
						"copy": &tools.FieldResolve{
							Resolve: func(p graphql.ResolveParams) (interface{}, error) {
								if !shouldEval(p.Context) {
									return lazyResolve(p)
								}
								src, ok := p.Args["src"].(FS)
								if !ok {
									return nil, fmt.Errorf("invalid copy src")
								}
								srcPath, ok := p.Args["srcPath"].(string)
								if !ok {
									srcPath = "/"
								}
								dst, ok := p.Args["dst"].(FS)
								if !ok {
									dst = FS{}
								}
								dstPath, ok := p.Args["dstPath"].(string)
								if !ok {
									dstPath = "/"
								}
								srcState, err := src.ToState()
								if err != nil {
									return nil, err
								}
								var dstState llb.State
								if dst.PB != nil {
									dstState, err = dst.ToState()
									if err != nil {
										return nil, err
									}
								} else {
									dstState = llb.Scratch()
								}
								llbdef, err := dstState.File(
									llb.Copy(srcState, srcPath, dstPath),
								).Marshal(p.Context, llb.Platform(getPlatform(p)))
								if err != nil {
									return nil, err
								}
								gw, err := getGatewayClient(p)
								if err != nil {
									return nil, err
								}
								_, err = gw.Solve(context.Background(), bkgw.SolveRequest{
									Evaluate:   true,
									Definition: llbdef.ToPB(),
								})
								if err != nil {
									return nil, err
								}
								return FS{PB: llbdef.ToPB()}, nil
							},
						},
					},
				},

				"Mutation": &tools.ObjectResolver{
					Fields: tools.FieldResolveMap{
						"import": &tools.FieldResolve{
							Resolve: func(p graphql.ResolveParams) (interface{}, error) {
								// TODO: make sure not duped
								pkgName, ok := p.Args["name"].(string)
								if !ok {
									return nil, fmt.Errorf("invalid package name")
								}

								if pkgName == "core" {
									return map[string]interface{}{
										"name":       pkgName,
										"schema":     daggerPackages["core"].Schema.TypeDefs.(string),
										"operations": coreOperations,
									}, nil
								}

								fs, ok := p.Args["fs"].(FS)
								if !ok {
									return nil, fmt.Errorf("invalid fs")
								}

								gw, err := getGatewayClient(p)
								if err != nil {
									return nil, err
								}
								res, err := gw.Solve(context.Background(), bkgw.SolveRequest{
									Evaluate:   true,
									Definition: fs.PB,
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

								// TODO: hacks: include the FS scalar in the schema so it's valid in isolation
								parsedSchema := parsed.TypeDefs.(string)
								parsedSchema = "scalar FS\n\n" + parsedSchema

								return map[string]interface{}{
									"name":       pkgName,
									"fs":         fs,
									"schema":     parsedSchema,
									"operations": string(operationsBytes),
								}, nil
							},
						},
						"readfile": &tools.FieldResolve{
							Resolve: func(p graphql.ResolveParams) (interface{}, error) {
								fs, ok := p.Args["fs"].(FS)
								if !ok {
									return nil, fmt.Errorf("invalid fs")
								}
								path, ok := p.Args["path"].(string)
								if !ok {
									return nil, fmt.Errorf("invalid path")
								}
								gw, err := getGatewayClient(p)
								if err != nil {
									return nil, err
								}
								res, err := gw.Solve(context.Background(), bkgw.SolveRequest{
									Evaluate:   true,
									Definition: fs.PB,
								})
								if err != nil {
									return nil, err
								}
								ref, err := res.SingleRef()
								if err != nil {
									return nil, err
								}
								outputBytes, err := ref.ReadFile(p.Context, bkgw.ReadRequest{
									Filename: path,
								})
								if err != nil {
									return nil, err
								}
								return string(outputBytes), nil
							},
						},
						"readsecret": &tools.FieldResolve{
							Resolve: func(p graphql.ResolveParams) (interface{}, error) {
								id, ok := p.Args["id"].(string)
								if !ok {
									return nil, fmt.Errorf("invalid secret id")
								}
								secrets := getSecrets(p)
								if secrets == nil {
									return nil, fmt.Errorf("no secrets")
								}
								secret, ok := secrets[id]
								if !ok {
									return nil, fmt.Errorf("no secret with id %s", id)
								}
								return secret, nil
							},
						},
						"clientdir": &tools.FieldResolve{
							Resolve: func(p graphql.ResolveParams) (interface{}, error) {
								id, ok := p.Args["id"].(string)
								if !ok {
									return nil, fmt.Errorf("invalid clientdir id")
								}
								llbdef, err := llb.Local(
									id,
									// TODO: better shared key hint?
									llb.SharedKeyHint(id),
									// FIXME: should not be hardcoded
									llb.ExcludePatterns([]string{"**/node_modules"}),
								).Marshal(p.Context, llb.LocalUniqueID(id))
								if err != nil {
									return nil, err
								}
								return FS{PB: llbdef.ToPB()}, nil
							},
						},
					},
				},
				"FS": &tools.ScalarResolver{
					Serialize: func(value interface{}) interface{} {
						switch v := value.(type) {
						case FS:
							fsbytes, err := v.MarshalText()
							if err != nil {
								panic(err)
							}
							return string(fsbytes)
						case string:
							return v
						default:
							panic(fmt.Sprintf("unexpected fs type %T", v))
						}
					},
					ParseValue: func(value interface{}) interface{} {
						switch v := value.(type) {
						case string:
							var fs FS
							if err := fs.UnmarshalText([]byte(v)); err != nil {
								panic(err)
							}
							return fs
						default:
							panic(fmt.Sprintf("unexpected fs value type %T", v))
						}
					},
					ParseLiteral: func(valueAST ast.Value) interface{} {
						switch valueAST := valueAST.(type) {
						case *ast.StringValue:
							var fs FS
							if err := fs.UnmarshalText([]byte(valueAST.Value)); err != nil {
								panic(err)
							}
							return fs
						default:
							panic(fmt.Sprintf("unexpected fs literal type: %T", valueAST))
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

const coreSchema = `
scalar FS

type CoreImage {
	fs: FS!
}

input CoreMount {
	path: String!
	fs: FS!
}
input CoreExecInput {
	mounts: [CoreMount!]!
	args: [String!]!
	workdir: String
}
type CoreExec {
	root: FS!
	getMount(path: String!): FS!
}

type Core {
	image(ref: String!): CoreImage
	exec(input: CoreExecInput!): CoreExec
	dockerfile(context: FS!, dockerfileName: String): FS!
	copy(src: FS!, srcPath: String, dst: FS, dstPath: String): FS!
}

type Query {
	core: Core!
	source: Source!
}

type Package {
	name: String!
	fs: FS
	schema: String!
	operations: String!
}

type Mutation {
	import(name: String!, fs: FS): Package
	readfile(fs: FS!, path: String!): String
	clientdir(id: String!): FS
	readsecret(id: String!): String
}

type Exec {
	fs: Filesystem!
	stdout(lines: Int): String
	stderr(lines: Int): String
	exitCode: Int
}

type Source {
	image(ref: String!): Filesystem!
	git(remote: String!, ref: String): Filesystem!
}

type Filesystem {
	id: ID!
	exec(args: [String!]): Exec!
	dockerbuild(dockerfile: String): Filesystem!
	file(path: String!, lines: Int): String
}
`

// TODO: add the rest of the operations
const coreOperations = `
query Image($ref: String!) {
  core {
    image(ref: $ref) {
      fs
    }
  }
}

query Exec($input: CoreExecInput!) {
  core {
    exec(input: $input) {
      root
    }
  }
}

query Dockerfile($context: FS!, $dockerfileName: String!) {
  core {
    dockerfile(context: $context, dockerfileName: $dockerfileName)
  }
}

mutation Import($name: String!, $fs: FS!) {
  import(name: $name, fs: $fs) {
    name
    fs
  }
}
`

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
