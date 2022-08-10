package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tools "github.com/bhoriuchi/graphql-go-tools"
	"github.com/containerd/containerd/platforms"
	"github.com/dagger/cloak/core/shim"
	"github.com/graphql-go/graphql"
	"github.com/moby/buildkit/client/llb"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

type FSID string

type Filesystem struct {
	ID FSID `json:"id"`
}

func (f *Filesystem) ToDefinition() (*pb.Definition, error) {
	jsonBytes := make([]byte, base64.StdEncoding.DecodedLen(len(f.ID)))
	n, err := base64.StdEncoding.Decode(jsonBytes, []byte(f.ID))
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal fs bytes: %v", err)
	}
	jsonBytes = jsonBytes[:n]
	var result pb.Definition
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %v: %s", err, string(jsonBytes))
	}
	return &result, nil
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

func (f *Filesystem) ReadFile(ctx context.Context, gw bkgw.Client, path string) ([]byte, error) {
	def, err := f.ToDefinition()
	if err != nil {
		return nil, err
	}

	res, err := gw.Solve(ctx, bkgw.SolveRequest{
		Definition: def,
	})
	if err != nil {
		return nil, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}
	outputBytes, err := ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: path,
	})
	if err != nil {
		return nil, err
	}
	return outputBytes, nil
}

func newFilesystem(def *llb.Definition) *Filesystem {
	jsonBytes, err := json.Marshal(def.ToPB())
	if err != nil {
		panic(err)
	}
	b64Bytes := make([]byte, base64.StdEncoding.EncodedLen(len(jsonBytes)))
	base64.StdEncoding.Encode(b64Bytes, jsonBytes)
	return &Filesystem{
		ID: FSID(b64Bytes),
	}
}

var coreResolver = &tools.ObjectResolver{
	Fields: tools.FieldResolveMap{
		"filesystem": &tools.FieldResolve{
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				id, ok := p.Args["id"].(FSID)
				if !ok {
					return nil, fmt.Errorf("missing id")
				}
				return &Filesystem{ID: id}, nil
			},
		},
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

		"clientdir": &tools.FieldResolve{
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				id, ok := p.Args["id"].(string)
				if !ok {
					return nil, fmt.Errorf("invalid clientdir id")
				}
				// copy to scratch to avoid making buildkit's snapshot of the local dir immutable,
				// which makes it unable to reused, which in turn creates cache invalidations
				// TODO: this should be optional, the above issue can also be avoided w/ readonly
				// mount when possible
				llbdef, err := llb.Scratch().File(llb.Copy(llb.Local(
					id,
					// TODO: better shared key hint?
					llb.SharedKeyHint(id),
					// FIXME: should not be hardcoded
					llb.ExcludePatterns([]string{"**/node_modules"}),
				), "/", "/")).Marshal(p.Context, llb.LocalUniqueID(id))
				if err != nil {
					return nil, err
				}
				return newFilesystem(llbdef), nil
			},
		},

		"secret": &tools.FieldResolve{
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

				fs, ok := p.Source.(*Filesystem)
				if !ok {
					// TODO: when returned by user actions, Filesystem is just a map[string]interface{}, need to fix, hack for now:
					m, ok := p.Source.(map[string]interface{})
					if !ok {
						return nil, fmt.Errorf("invalid source")
					}
					id, ok := m["id"].(string)
					if !ok {
						return nil, fmt.Errorf("invalid source")
					}
					fs = &Filesystem{
						ID: FSID(id),
					}
				}

				st, err := fs.ToState()
				if err != nil {
					return nil, err
				}

				input, ok := p.Args["input"].(map[string]interface{})
				if !ok {
					return nil, fmt.Errorf("invalid input")
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

				mounts := map[string]*Filesystem{}
				rawMounts, ok := input["mounts"].([]interface{})
				if ok {
					for _, rawMount := range rawMounts {
						mount, ok := rawMount.(map[string]interface{})
						if !ok {
							return nil, fmt.Errorf("invalid mount")
						}
						path, ok := mount["path"].(string)
						if !ok {
							return nil, fmt.Errorf("invalid mount path")
						}
						fsid, ok := mount["fs"].(FSID)
						if !ok {
							return nil, fmt.Errorf("invalid mount fsid")
						}
						mounts[path] = &Filesystem{ID: FSID(fsid)}
					}
				}

				shim, err := shim.Build(p.Context, gw, getPlatform(p))
				if err != nil {
					return nil, err
				}

				runOpt := []llb.RunOption{
					llb.Args(append([]string{"/_shim"}, args...)),
					llb.AddMount("/_shim", shim, llb.SourcePath("/_shim")),
					llb.Dir(workdir),
				}

				execState := st.Run(runOpt...)

				metadataDef, err := execState.AddMount("/dagger", llb.Scratch()).Marshal(p.Context, llb.Platform(getPlatform(p)))
				if err != nil {
					return nil, err
				}

				for path, mount := range mounts {
					state, err := mount.ToState()
					if err != nil {
						return nil, err
					}
					_ = execState.AddMount(path, state)
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

				for path := range mounts {
					mountDef, err := execState.GetMount(path).Marshal(p.Context, llb.Platform(getPlatform(p)))
					if err != nil {
						return nil, err
					}
					mounts[path] = newFilesystem(mountDef)
				}

				return map[string]interface{}{
					"fs":       newFilesystem(llbdef),
					"metadata": newFilesystem(metadataDef),
					"mounts":   mounts,
				}, nil
			},
		},
		"file": &tools.FieldResolve{
			Resolve: func(p graphql.ResolveParams) (r interface{}, rerr error) {
				fs, ok := p.Source.(*Filesystem)
				if !ok {
					// TODO: when returned by user actions, Filesystem is just a map[string]interface{}, need to fix, hack for now:
					m, ok := p.Source.(map[string]interface{})
					if !ok {
						return nil, fmt.Errorf("invalid source type: %T", p.Source)
					}
					id, ok := m["id"].(string)
					if !ok {
						return nil, fmt.Errorf("invalid source id: %T %v", p.Source, p.Source)
					}
					fs = &Filesystem{
						ID: FSID(id),
					}
				}

				path, ok := p.Args["path"].(string)
				if !ok {
					return nil, fmt.Errorf("invalid path")
				}
				gw, err := getGatewayClient(p)
				if err != nil {
					return nil, err
				}

				output, err := fs.ReadFile(p.Context, gw, path)
				if err != nil {
					return nil, fmt.Errorf("failed to read file: %v", err)
				}

				return truncate(string(output), p.Args), nil
			},
		},

		"dockerbuild": &tools.FieldResolve{
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				fs, ok := p.Source.(*Filesystem)
				if !ok {
					// TODO: when returned by user actions, Filesystem is just a map[string]interface{}, need to fix, hack for now:
					m, ok := p.Source.(map[string]interface{})
					if !ok {
						return nil, fmt.Errorf("invalid source")
					}
					id, ok := m["id"].(string)
					if !ok {
						return nil, fmt.Errorf("invalid source")
					}
					fs = &Filesystem{
						ID: FSID(id),
					}
				}

				def, err := fs.ToDefinition()
				if err != nil {
					return nil, err
				}

				dockerfileName, _ := p.Args["dockerfile"].(string)

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

var execResolver = &tools.ObjectResolver{
	Fields: tools.FieldResolveMap{
		"stdout": &tools.FieldResolve{
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				exec := p.Source.(map[string]interface{})
				fs := exec["metadata"].(*Filesystem)

				gw, err := getGatewayClient(p)
				if err != nil {
					return nil, err
				}

				output, err := fs.ReadFile(p.Context, gw, "/stdout")
				if err != nil {
					return nil, err
				}

				return truncate(string(output), p.Args), nil
			},
		},
		"stderr": &tools.FieldResolve{
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				exec := p.Source.(map[string]interface{})
				fs := exec["metadata"].(*Filesystem)

				gw, err := getGatewayClient(p)
				if err != nil {
					return nil, err
				}

				output, err := fs.ReadFile(p.Context, gw, "/stderr")
				if err != nil {
					return nil, err
				}

				return truncate(string(output), p.Args), nil
			},
		},

		"exitCode": &tools.FieldResolve{
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				exec := p.Source.(map[string]interface{})
				fs := exec["metadata"].(*Filesystem)

				gw, err := getGatewayClient(p)
				if err != nil {
					return nil, err
				}

				output, err := fs.ReadFile(p.Context, gw, "/exitCode")
				if err != nil {
					return nil, err
				}

				return strconv.Atoi(string(output))
			},
		},

		"mount": &tools.FieldResolve{
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				exec := p.Source.(map[string]interface{})
				mounts, ok := exec["mounts"].(map[string]*Filesystem)
				if !ok {
					return nil, fmt.Errorf("invalid source mounts")
				}
				path, ok := p.Args["path"].(string)
				if !ok {
					return nil, fmt.Errorf("invalid path")
				}
				mnt, ok := mounts[path]
				if !ok {
					return nil, fmt.Errorf("missing mount path")
				}
				return mnt, nil
			},
		},
	},
}

func truncate(s string, args map[string]interface{}) string {
	if lines, ok := args["lines"].(int); ok {
		l := strings.SplitN(s, "\n", lines+1)
		if lines > len(l) {
			lines = len(l)
		}
		return strings.Join(l[0:lines], "\n")
	}

	return s
}
