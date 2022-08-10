package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	schemaPath     = "/schema.graphql"
	operationsPath = "/operations.graphql"
	entrypointPath = "/entrypoint"

	daggerSockName = "dagger-sock"
	daggerSockPath = "/dagger.sock"

	fsMountPath  = "/mnt"
	tmpMountPath = "/tmp"

	inputMountPath = "/inputs"
	inputFile      = "/dagger.json"

	outputMountPath = "/outputs"
	outputFile      = "/dagger.json"
)

var _ router.ExecutableSchema = &remoteSchema{}

type remoteSchema struct {
	gw       bkgw.Client
	fs       *filesystem.Filesystem
	platform specs.Platform

	sdl        string
	operations string
	resolvers  router.Resolvers
}

func Load(ctx context.Context, gw bkgw.Client, platform specs.Platform, fs *filesystem.Filesystem) (router.ExecutableSchema, error) {
	sdl, err := fs.ReadFile(ctx, gw, schemaPath)
	if err != nil {
		return nil, err
	}
	operations, err := fs.ReadFile(ctx, gw, operationsPath)
	if err != nil {
		return nil, err
	}
	s := &remoteSchema{
		gw:         gw,
		fs:         fs,
		platform:   platform,
		sdl:        string(sdl),
		operations: string(operations),
		resolvers:  router.Resolvers{},
	}
	if err := s.parse(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *remoteSchema) Schema() string {
	return s.sdl
}

func (s *remoteSchema) Operations() string {
	return s.operations
}

func (s *remoteSchema) Resolvers() router.Resolvers {
	return s.resolvers
}

func (s *remoteSchema) parse() error {
	doc, err := parser.Parse(parser.ParseParams{Source: s.sdl})
	if err != nil {
		return err
	}
	for _, def := range doc.Definitions {
		var obj *ast.ObjectDefinition

		if def, ok := def.(*ast.ObjectDefinition); ok {
			obj = def
		}

		if def, ok := def.(*ast.TypeExtensionDefinition); ok {
			obj = def.Definition
		}

		if obj == nil {
			continue
		}

		objResolver := router.ObjectResolver{}
		s.resolvers[obj.Name.Value] = objResolver
		for _, field := range obj.Fields {
			// FIXME: This heuristic currently assigns a resolver to every field expecting arguments.
			if len(field.Arguments) == 0 {
				objResolver[field.Name.Value] = func(p graphql.ResolveParams) (any, error) {
					parent := make(map[string]interface{})
					if err := convertArg(p.Source, &parent); err != nil {
						return nil, err
					}
					if v, ok := parent[p.Info.FieldName]; ok {
						return v, nil
					}
					return struct{}{}, nil
				}
				continue
			}
			objResolver[field.Name.Value] = s.resolve
		}
	}
	return nil
}

func (s *remoteSchema) resolve(p graphql.ResolveParams) (any, error) {
	pathArray := p.Info.Path.AsArray()
	name := fmt.Sprintf("%+v", pathArray)
	lastPath := pathArray[len(pathArray)-1]

	inputMap := map[string]interface{}{
		"object": lastPath.(string),
		"args":   p.Args,
	}
	inputBytes, err := json.Marshal(inputMap)
	if err != nil {
		return nil, err
	}
	input := llb.Scratch().File(llb.Mkfile(inputFile, 0644, inputBytes))

	fsState, err := s.fs.ToState()
	if err != nil {
		return nil, err
	}

	st := fsState.Run(
		llb.Args([]string{entrypointPath}),
		llb.AddSSHSocket(
			llb.SSHID(daggerSockName),
			llb.SSHSocketTarget(daggerSockPath),
		),
		llb.AddMount(inputMountPath, input, llb.Readonly),
		llb.AddMount(tmpMountPath, llb.Scratch(), llb.Tmpfs()),
		llb.ReadonlyRootFS(),
	)

	// TODO: /mnt should maybe be configurable?
	for path, fsid := range collectFSPaths(p.Args, fsMountPath, nil) {
		fs := filesystem.New(fsid)
		fsState, err := fs.ToState()
		if err != nil {
			return nil, err
		}
		// TODO: it should be possible for this to be outputtable by the action; the only question
		// is how to expose that ability in a non-confusing way, just needs more thought
		st.AddMount(path, fsState, llb.ForceNoOutput)
	}

	outputMnt := st.AddMount(outputMountPath, llb.Scratch())
	outputDef, err := outputMnt.Marshal(p.Context, llb.Platform(s.platform), llb.WithCustomName(name))
	if err != nil {
		return nil, err
	}

	res, err := s.gw.Solve(p.Context, bkgw.SolveRequest{
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
		Filename: outputFile,
	})
	if err != nil {
		return nil, err
	}
	var output interface{}
	if err := json.Unmarshal(outputBytes, &output); err != nil {
		return nil, fmt.Errorf("failed to unmarshal output: %w", err)
	}
	return output, nil
}

func collectFSPaths(arg interface{}, curPath string, fsPaths map[string]filesystem.FSID) map[string]filesystem.FSID {
	if fsPaths == nil {
		fsPaths = make(map[string]filesystem.FSID)
	}

	switch arg := arg.(type) {
	case filesystem.FSID:
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

// TODO:(sipsma) put in shared util? this is duped with core package
func convertArg(arg any, dest any) error {
	marshalled, err := json.Marshal(arg)
	if err != nil {
		return err
	}
	return json.Unmarshal(marshalled, dest)
}
