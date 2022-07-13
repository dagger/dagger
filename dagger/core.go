package dagger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

const (
	daggerSockName = "dagger-sock"
)

// FS is either llb representing the filesystem or a graphql query for obtaining that llb
// This is opaque to clients; to them FS is a scalar.
type FS struct {
	PB    *pb.Definition `json:"pb,omitempty"`
	Query string         `json:"query,omitempty"` // TODO: an actual graphql type would be better
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

func (fs FS) Evaluate(ctx context.Context) (FS, error) {
	for fs.PB == nil {
		// TODO: guard against accidental infinite loop
		// this loop is where the "stack" is unwound, should later add debug info that traces each query leading to the final result
		if fs.Query == "" {
			return FS{}, fmt.Errorf("invalid fs: missing query")
		}
		result := graphql.Do(graphql.Params{
			Schema:        schema,
			RequestString: fs.Query,
			Context:       withEval(ctx),
		})
		if result.HasErrors() {
			return FS{}, fmt.Errorf("eval errors: %+v", result.Errors)
		}

		// Extract the queried field out of the result
		// TODO: this is hilariously hacky, only looks for "fs", there obviously has to be a better way, just don't know where the graphql parsing utils are yet
		resultBytes, err := json.Marshal(result.Data)
		if err != nil {
			return FS{}, err
		}
		var resultMap map[string]interface{}
		if err := json.Unmarshal(resultBytes, &resultMap); err != nil {
			return FS{}, err
		}
		var found bool
		for !found {
			if len(resultMap) != 1 {
				return FS{}, fmt.Errorf("unhandled result: %+v", resultMap)
			}
			for k, v := range resultMap {
				if k == "fs" {
					if err := json.Unmarshal([]byte(v.(string)), &fs); err != nil {
						return FS{}, err
					}
					found = true
					break
				} else {
					resultMap = v.(map[string]interface{})
				}
			}
		}
	}
	return fs, nil
}

func (fs *FS) UnmarshalJSON(b []byte) error {
	// support marshaling from struct or from struct serialized to string (TODO: something's weird here with the graphql serialization, probably more sane way of doing this)
	var inner struct {
		PB    *pb.Definition `json:"pb,omitempty"`
		Query string         `json:"query,omitempty"`
	}
	if err := json.Unmarshal(b, &inner); err == nil {
		fs.PB = inner.PB
		fs.Query = inner.Query
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(str), &inner); err != nil {
		return err
	}
	fs.PB = inner.PB
	fs.Query = inner.Query
	return nil
}

var FSType = graphql.NewScalar(graphql.ScalarConfig{
	Name: "fs",
	Serialize: func(value interface{}) interface{} {
		bytes, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		return string(bytes)
	},
	ParseValue: func(value interface{}) interface{} {
		var fs FS
		var input string
		switch value := value.(type) {
		case string:
			input = value
		case *string:
			input = *value
		default:
			panic(fmt.Sprintf("unsupported type: %T", value))
		}
		if err := json.Unmarshal([]byte(input), &fs); err != nil {
			panic(err)
		}
		return fs
	},
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.StringValue:
			var fs FS
			if err := json.Unmarshal([]byte(valueAST.Value), &fs); err != nil {
				panic(err)
			}
			return fs
		default:
			panic(fmt.Sprintf("unsupported type: %T", valueAST))
		}
	},
})

type Image struct {
	FS FS `json:"fs"`
}

var ImageType = graphql.NewObject(
	graphql.ObjectConfig{
		Name: "image",
		Fields: graphql.Fields{
			"fs": &graphql.Field{
				Type: FSType,
			},
		},
	},
)

type Exec struct {
	FS FS `json:"fs"`
}

var ExecType = graphql.NewObject(
	graphql.ObjectConfig{
		Name: "exec",
		Fields: graphql.Fields{
			"fs": &graphql.Field{
				Type: FSType,
			},
		},
	},
)

type Core struct {
	Image Image `json:"image"`
	Exec  Exec  `json:"exec"`
}

type CoreResult struct {
	Core Core `json:"core"`
}

var CoreType = graphql.NewObject(
	graphql.ObjectConfig{
		Name: "core",
		Fields: graphql.Fields{
			"image": &graphql.Field{
				Type:        ImageType,
				Description: "An image from a registry",
				Args: graphql.FieldConfigArgument{
					"ref": &graphql.ArgumentConfig{
						Type: graphql.String, // TODO: lazy
					},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					if !shouldEval(p.Context) {
						return Image{FS: FS{Query: getPayload(p.Context)}}, nil
					}
					ref, ok := p.Args["ref"].(string)
					if !ok {
						return nil, fmt.Errorf("invalid ref")
					}
					llbdef, err := llb.Image(ref).Marshal(p.Context)
					if err != nil {
						return nil, err
					}
					return Image{FS: FS{PB: llbdef.ToPB()}}, nil
				},
			},
			"exec": &graphql.Field{
				Type: ExecType,
				Args: graphql.FieldConfigArgument{
					"fs": &graphql.ArgumentConfig{
						Type: FSType,
					},
					"args": &graphql.ArgumentConfig{
						Type: graphql.NewList(graphql.String), // TODO: make lazy
					},
					// TODO: more like workdir, extra mounts, etc.
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					if !shouldEval(p.Context) {
						return Exec{FS: FS{Query: getPayload(p.Context)}}, nil
					}
					fs, ok := p.Args["fs"].(FS)
					if !ok {
						return nil, fmt.Errorf("invalid fs")
					}
					rawArgs, ok := p.Args["args"].([]interface{})
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
					fs, err := fs.Evaluate(p.Context)
					if err != nil {
						return nil, err
					}
					fsState, err := fs.ToState()
					if err != nil {
						return nil, err
					}
					llbdef, err := fsState.Run(llb.Args(args)).Root().Marshal(p.Context)
					if err != nil {
						return nil, err
					}
					return Exec{FS: FS{PB: llbdef.ToPB()}}, nil
				},
			},
		},
	},
)

// TODO: this should be loaded, just inlining for now to test idea out
type AlpineBuildInput struct {
	Pkgs []string `json:"pkgs"`
}

type AlpineBuild struct {
	FS FS `json:"fs"`
}

var AlpineBuildType = graphql.NewObject(
	graphql.ObjectConfig{
		Name: "build",
		Fields: graphql.Fields{
			"fs": &graphql.Field{
				Type: FSType,
			},
		},
	},
)

type Alpine struct {
	Build AlpineBuild `json:"build"`
}

type AlpineResult struct {
	Alpine Alpine `json:"alpine"`
}

var AlpineType = graphql.NewObject(
	graphql.ObjectConfig{
		Name: "alpine",
		Fields: graphql.Fields{
			"build": &graphql.Field{
				Type: AlpineBuildType,
				Args: graphql.FieldConfigArgument{
					"pkgs": &graphql.ArgumentConfig{
						Type: graphql.NewList(graphql.String),
					},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					if !shouldEval(p.Context) {
						return AlpineBuild{FS: FS{Query: getPayload(p.Context)}}, nil
					}

					inputBytes, err := json.Marshal(p.Args)
					if err != nil {
						return nil, err
					}
					input := llb.Scratch().File(llb.Mkfile("/dagger.json", 0644, inputBytes))
					st := llb.Image("localhost:5555/dagger:alpine").Run(
						llb.Args([]string{"/usr/local/bin/dagger", "-a", "build"}),
						llb.AddSSHSocket(
							llb.SSHID(daggerSockName),
							llb.SSHSocketTarget("/dagger.sock"),
						),
						llb.AddMount("/inputs", input, llb.Readonly),
						llb.ReadonlyRootFS(),
					)
					outputMnt := st.AddMount("/outputs", llb.Scratch())
					outputDef, err := outputMnt.Marshal(p.Context)
					if err != nil {
						return nil, err
					}

					gw, err := getGatewayClient(p.Context)
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

					var result AlpineBuild
					if err := json.Unmarshal(outputBytes, &result); err != nil {
						return nil, err
					}
					return result, nil
				},
			},
		},
	},
)

var queryType = graphql.NewObject(
	graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"core": &graphql.Field{
				Type:        CoreType,
				Description: "Dagger Core Actions",
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					return struct{}{}, nil
				},
			},
			"alpine": &graphql.Field{
				Type: AlpineType,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					return struct{}{}, nil
				},
			},
		},
	})

type DaggerPackage struct {
	Name string `json:"name"`
	// TODO: more like version
}

var PackageType = graphql.NewObject(
	graphql.ObjectConfig{
		Name: "package",
		Fields: graphql.Fields{
			"name": &graphql.Field{
				Type: graphql.String,
			},
		},
	},
)

type ReadFile struct {
	Contents string `json:"contents"`
}

var ReadFileType = graphql.NewObject(
	graphql.ObjectConfig{
		Name: "readfile",
		Fields: graphql.Fields{
			"contents": &graphql.Field{
				Type: graphql.String,
			},
		},
	},
)

type EvaluateResult struct {
	Evaluate FS `json:"evaluate"`
}

// TODO: obviously shouldn't be using a global var, pass through resolve context, make sure synchronization is handled
var schema graphql.Schema

var mutationType = graphql.NewObject(
	graphql.ObjectConfig{
		Name: "Mutation",
		Fields: graphql.Fields{
			"import": &graphql.Field{
				Type: PackageType,
				Args: graphql.FieldConfigArgument{
					"ref": &graphql.ArgumentConfig{
						Type: graphql.String,
					},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					// TODO: resolve ref to a package, merge the schema in (make sure not duped)
					return nil, fmt.Errorf("import not implemented yet")
				},
			},
			"evaluate": &graphql.Field{
				Type: FSType,
				Args: graphql.FieldConfigArgument{
					"fs": &graphql.ArgumentConfig{
						Type: FSType,
					},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					fs, ok := p.Args["fs"].(FS)
					if !ok {
						return nil, fmt.Errorf("invalid fs")
					}
					return fs.Evaluate(p.Context)
				},
			},
			"readfile": &graphql.Field{
				Type: ReadFileType,
				Args: graphql.FieldConfigArgument{
					"fs": &graphql.ArgumentConfig{
						Type: FSType,
					},
					"path": &graphql.ArgumentConfig{
						Type: graphql.String,
					},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					fs, ok := p.Args["fs"].(FS)
					if !ok {
						return nil, fmt.Errorf("invalid fs")
					}
					path, ok := p.Args["path"].(string)
					if !ok {
						return nil, fmt.Errorf("invalid path")
					}
					fs, err := fs.Evaluate(p.Context)
					if err != nil {
						return nil, err
					}
					gw, err := getGatewayClient(p.Context)
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
					return ReadFile{Contents: string(outputBytes)}, nil
				},
			},
		},
	})

type evalKey struct{}

func withEval(ctx context.Context) context.Context {
	return context.WithValue(ctx, evalKey{}, true)
}

func shouldEval(ctx context.Context) bool {
	val, ok := ctx.Value(evalKey{}).(bool)
	return ok && val
}

func init() {
	var err error
	schema, err = graphql.NewSchema(graphql.SchemaConfig{
		Query:    queryType,
		Mutation: mutationType,
	})
	if err != nil {
		panic(err)
	}
}
