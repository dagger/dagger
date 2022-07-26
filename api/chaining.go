package api

import (
	"context"
	"fmt"

	tools "github.com/bhoriuchi/graphql-go-tools"
	"github.com/containerd/containerd/platforms"
	"github.com/graphql-go/graphql"
	"github.com/moby/buildkit/client/llb"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

type Filesystem struct {
	ID string `json:"id"`
}

func (f *Filesystem) ToDefinition() (*pb.Definition, error) {
	var fs FS
	if err := fs.UnmarshalText([]byte(f.ID)); err != nil {
		return nil, err
	}
	return fs.PB, nil
}

func (f *Filesystem) ToState() (llb.State, error) {
	def, err := f.ToDefinition()
	if err != nil {
		return llb.State{}, nil
	}
	defop, err := llb.NewDefinitionOp(def)
	if err != nil {
		return llb.State{}, err
	}
	return llb.NewState(defop), nil
}

func newFilesystem(def *llb.Definition) *Filesystem {
	fs := FS{PB: def.ToPB()}
	fsbytes, err := fs.MarshalText()
	if err != nil {
		panic(err)
	}
	return &Filesystem{
		ID: string(fsbytes),
	}
}

var sourceResolver = &tools.ObjectResolver{
	Fields: tools.FieldResolveMap{
		"image": &tools.FieldResolve{
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
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

				return newFilesystem(llbdef), nil
			},
		},

		"git": &tools.FieldResolve{
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				remote, ok := p.Args["remote"].(string)
				if !ok {
					return nil, fmt.Errorf("missing remote")
				}

				ref, ok := p.Args["ref"].(string)
				if !ok {
					ref = ""
				}

				st := llb.Git(remote, ref)

				llbdef, err := st.Marshal(p.Context, llb.Platform(getPlatform(p)))
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

				return newFilesystem(llbdef), nil
			},
		},
	},
}

var filesystemResolver = &tools.ObjectResolver{
	Fields: tools.FieldResolveMap{
		"exec": &tools.FieldResolve{
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				gw, err := getGatewayClient(p)
				if err != nil {
					return nil, err
				}

				filesystem := p.Source.(*Filesystem)
				st, err := filesystem.ToState()
				if err != nil {
					return nil, err
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
				st = st.Run(llb.Args(args)).Root()

				llbdef, err := st.Marshal(p.Context, llb.Platform(getPlatform(p)))
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

				return newFilesystem(llbdef), nil
			},
		},
		"file": &tools.FieldResolve{
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				filesystem := p.Source.(*Filesystem)
				def, err := filesystem.ToDefinition()
				if err != nil {
					return nil, err
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
					Definition: def,
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

		"dockerfile": &tools.FieldResolve{
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				filesystem := p.Source.(*Filesystem)
				def, err := filesystem.ToDefinition()
				if err != nil {
					return nil, err
				}

				var dockerfileName string
				rawDockerfileName, ok := p.Args["name"]
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
					dockerfilebuilder.DefaultLocalNameContext:    def,
					dockerfilebuilder.DefaultLocalNameDockerfile: def,
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

				return newFilesystem(llbdef), nil
			},
		},
	},
}
