package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/moby/buildkit/client/llb"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
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

// RemoteSchema holds the schema, operations and other project configuration
// of an extension, but has not yet been "compiled" with an SDK to an executable
// extension. This allows obtaining the project metadata without necessarily
// being able to build it yet.
type RemoteSchema struct {
	gw         bkgw.Client
	platform   specs.Platform
	projectFS  *filesystem.Filesystem
	configPath string

	name         string
	sdl          string
	operations   string
	dependencies []*RemoteSchema
}

func Load(ctx context.Context, gw bkgw.Client, platform specs.Platform, projectFS *filesystem.Filesystem, configPath string) (*RemoteSchema, error) {
	cloakCfgBytes, err := projectFS.ReadFile(ctx, gw, configPath)
	if err != nil {
		return nil, err
	}
	cloakCfg, err := ParseConfig(cloakCfgBytes)
	if err != nil {
		return nil, err
	}

	// schema and operations are both optional, ignore file not found errors
	sdl, err := projectFS.ReadFile(ctx, gw, filepath.Join(filepath.Dir(configPath), schemaPath))
	if err != nil && !isGatewayFileNotFound(err) {
		return nil, err
	}
	operations, err := projectFS.ReadFile(ctx, gw, filepath.Join(filepath.Dir(configPath), operationsPath))
	if err != nil && !isGatewayFileNotFound(err) {
		return nil, err
	}

	// verify the schema is valid
	if len(sdl) > 0 {
		if _, err := parser.Parse(parser.ParseParams{Source: sdl}); err != nil {
			return nil, err
		}
	}

	s := &RemoteSchema{
		gw:         gw,
		platform:   platform,
		projectFS:  projectFS,
		configPath: configPath,
		name:       cloakCfg.Name,
		sdl:        string(sdl),
		operations: string(operations),
	}

	for _, ext := range cloakCfg.Extensions {
		// TODO:(sipsma) support more than just Local
		if ext.Local != "" {
			depConfigPath := filepath.Join(filepath.Dir(configPath), ext.Local)
			depSchema, err := Load(ctx, gw, platform, projectFS, depConfigPath)
			if err != nil {
				return nil, err
			}
			s.dependencies = append(s.dependencies, depSchema)
		}
	}

	return s, nil
}

func (s *RemoteSchema) Name() string {
	return s.name
}

func (s *RemoteSchema) Schema() string {
	return s.sdl
}

func (s *RemoteSchema) Operations() string {
	return s.operations
}

func (s *RemoteSchema) Dependencies() []*RemoteSchema {
	return s.dependencies
}

func (s RemoteSchema) Compile(ctx context.Context) (*CompiledRemoteSchema, error) {
	// TODO:(sipsma) hardcoding use of a "dockerfile sdk", should obviously be generalized
	def, err := s.projectFS.ToDefinition()
	if err != nil {
		return nil, err
	}

	opts := map[string]string{
		"platform": platforms.Format(s.platform),
		"filename": filepath.Join(filepath.Dir(s.configPath), "Dockerfile"),
	}
	inputs := map[string]*pb.Definition{
		dockerfilebuilder.DefaultLocalNameContext:    def,
		dockerfilebuilder.DefaultLocalNameDockerfile: def,
	}
	res, err := s.gw.Solve(ctx, bkgw.SolveRequest{
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

	fs, err := filesystem.FromState(ctx, st, s.platform)
	if err != nil {
		return nil, err
	}

	compiled := &CompiledRemoteSchema{
		RemoteSchema: s,
		runtimeFS:    fs,
		resolvers:    router.Resolvers{},
	}

	doc, err := parser.Parse(parser.ParseParams{Source: s.sdl})
	if err != nil {
		return nil, err
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
		compiled.resolvers[obj.Name.Value] = objResolver
		for _, field := range obj.Fields {
			objResolver[field.Name.Value] = compiled.resolve
		}
	}

	for _, dep := range s.dependencies {
		// TODO:(sipsma) deduplicate recompiling same dep (current behavior is O(really bad))
		// TODO: also guard against infinite recursion
		depCompiled, err := dep.Compile(ctx)
		if err != nil {
			return nil, err
		}
		compiled.dependencies = append(compiled.dependencies, depCompiled)
	}

	return compiled, nil
}

// CompiledRemoteSchema is the compiled version of RemoteSchema where the
// SDK has built the input project FS into an executable extension.
type CompiledRemoteSchema struct {
	RemoteSchema
	runtimeFS    *filesystem.Filesystem
	dependencies []router.ExecutableSchema
	resolvers    router.Resolvers
}

var _ router.ExecutableSchema = &CompiledRemoteSchema{}

func (s *CompiledRemoteSchema) Resolvers() router.Resolvers {
	return s.resolvers
}

func (s *CompiledRemoteSchema) Dependencies() []router.ExecutableSchema {
	return s.dependencies
}

func (s *CompiledRemoteSchema) RuntimeFS() *filesystem.Filesystem {
	return s.runtimeFS
}

func (s *CompiledRemoteSchema) resolve(p graphql.ResolveParams) (any, error) {
	pathArray := p.Info.Path.AsArray()
	name := fmt.Sprintf("%+v", pathArray)

	resolverName := fmt.Sprintf("%s.%s", p.Info.ParentType.Name(), p.Info.FieldName)
	inputMap := map[string]interface{}{
		"resolver": resolverName,
		"args":     p.Args,
		"parent":   p.Source,
	}
	inputBytes, err := json.Marshal(inputMap)
	if err != nil {
		return nil, err
	}
	input := llb.Scratch().File(llb.Mkfile(inputFile, 0644, inputBytes))

	fsState, err := s.runtimeFS.ToState()
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

func isGatewayFileNotFound(err error) bool {
	if err == nil {
		return false
	}
	// TODO:(sipsma) the underlying error type doesn't appear to be passed over grpc
	// from buildkit, so we have to resort to nasty substring checking, need a better way
	return strings.Contains(err.Error(), "no such file or directory")
}
