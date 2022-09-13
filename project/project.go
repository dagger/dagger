package project

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/singleflight"
)

const (
	schemaPath     = "/schema.graphql"
	entrypointPath = "/entrypoint"

	DaggerSockName = "dagger-sock"
	daggerSockPath = "/dagger.sock"

	fsMountPath  = "/mnt"
	tmpMountPath = "/tmp"

	inputMountPath = "/inputs"
	inputFile      = "/dagger.json"

	outputMountPath = "/outputs"
	outputFile      = "/dagger.json"
)

// RemoteSchema holds the schema and other configuration of an
// extension, but has not yet been "compiled" with an SDK to an executable
// extension. This allows obtaining the extension metadata without necessarily
// being able to build it yet.
type RemoteSchema struct {
	gw         bkgw.Client
	platform   specs.Platform
	contextFS  *filesystem.Filesystem
	configPath string

	router.LoadedSchema
	dependencies  []*RemoteSchema
	scripts       []*Script
	extensions    []*Extension
	sshAuthSockID string
}

func Load(ctx context.Context, gw bkgw.Client, platform specs.Platform, contextFS *filesystem.Filesystem, configPath string, sshAuthSockID string) (*RemoteSchema, error) {
	cfgBytes, err := contextFS.ReadFile(ctx, gw, configPath)
	if err != nil {
		return nil, err
	}
	cfg, err := ParseConfig(cfgBytes)
	if err != nil {
		return nil, err
	}

	s := &RemoteSchema{
		gw:            gw,
		platform:      platform,
		contextFS:     contextFS,
		configPath:    configPath,
		scripts:       cfg.Scripts,
		extensions:    cfg.Extensions,
		sshAuthSockID: sshAuthSockID,
	}

	sourceSchemas := make([]router.LoadedSchema, len(cfg.Extensions))
	for i, ext := range cfg.Extensions {
		sdl, err := contextFS.ReadFile(ctx, gw, filepath.Join(
			filepath.Dir(configPath),
			ext.Path,
			schemaPath,
		))
		if err != nil {
			return nil, err
		}
		ext.Schema = string(sdl)

		sourceSchemas[i] = router.StaticSchema(router.StaticSchemaParams{
			Schema: ext.Schema,
		})
	}
	s.LoadedSchema = router.MergeLoadedSchemas(cfg.Name, sourceSchemas...)

	// TODO:(sipsma) guard against infinite recursion
	// TODO:(sipsma) deduplicate load of same dependencies (same as compile)
	for _, dep := range cfg.Dependencies {
		// TODO:(sipsma) ensure only one source is specified
		switch {
		case dep.Local != "":
			depConfigPath := filepath.Join(filepath.Dir(configPath), dep.Local)
			depSchema, err := Load(ctx, gw, platform, contextFS, depConfigPath, sshAuthSockID)
			if err != nil {
				return nil, err
			}
			s.dependencies = append(s.dependencies, depSchema)
		case dep.Git != nil:
			var opts []llb.GitOption
			if sshAuthSockID != "" {
				opts = append(opts, llb.MountSSHSock(sshAuthSockID))
			}
			gitFS, err := filesystem.FromState(ctx, llb.Git(dep.Git.Remote, dep.Git.Ref, opts...), platform)
			if err != nil {
				return nil, err
			}
			depSchema, err := Load(ctx, gw, platform, gitFS, dep.Git.Path, sshAuthSockID)
			if err != nil {
				return nil, err
			}
			s.dependencies = append(s.dependencies, depSchema)
		}
	}

	return s, nil
}

func (s *RemoteSchema) Dependencies() []*RemoteSchema {
	return s.dependencies
}

func (s *RemoteSchema) Scripts() []*Script {
	return s.scripts
}

func (s *RemoteSchema) Extensions() []*Extension {
	return s.extensions
}

func (s RemoteSchema) Compile(ctx context.Context, cache map[string]*CompiledRemoteSchema, l *sync.RWMutex, sf *singleflight.Group) (*CompiledRemoteSchema, error) {
	res, err, _ := sf.Do(s.Name(), func() (interface{}, error) {
		// if we have already compiled a schema with this name, return it
		// TODO:(sipsma) should check that schema is actually the same, error out if not
		l.RLock()
		cached, ok := cache[s.Name()]
		l.RUnlock()
		if ok {
			return cached, nil
		}

		compiled := &CompiledRemoteSchema{
			RemoteSchema: s,
			resolvers:    router.Resolvers{},
		}

		for _, ext := range s.extensions {
			runtimeFS, err := s.Runtime(ctx, ext)
			if err != nil {
				return nil, err
			}
			doc, err := parser.Parse(parser.ParseParams{Source: s.Schema()})
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
					objResolver[field.Name.Value] = compiled.resolver(runtimeFS)
				}
			}
		}

		for _, dep := range s.dependencies {
			// TODO:(sipsma) guard against infinite recursion
			depCompiled, err := dep.Compile(ctx, cache, l, sf)
			if err != nil {
				return nil, err
			}
			compiled.dependencies = append(compiled.dependencies, depCompiled)
		}

		l.Lock()
		cache[s.Name()] = compiled
		l.Unlock()
		return compiled, nil
	})
	if err != nil {
		return nil, err
	}
	return res.(*CompiledRemoteSchema), nil
}

// CompiledRemoteSchema is the compiled version of RemoteSchema where the
// SDK has built its input into an executable extension.
type CompiledRemoteSchema struct {
	RemoteSchema
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

func (s *CompiledRemoteSchema) resolver(runtimeFS *filesystem.Filesystem) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
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

		fsState, err := runtimeFS.ToState()
		if err != nil {
			return nil, err
		}

		st := fsState.Run(
			llb.Args([]string{entrypointPath}),
			llb.AddSSHSocket(
				llb.SSHID(DaggerSockName),
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

		// Mount in the parent type if it is a Filesystem
		// FIXME:(sipsma) got to be a better way than string matching parent type... But not easy
		// to just use go type matching because the parent result may be a Filesystem struct or
		// an untyped map[string]interface{}.
		if p.Info.ParentType.Name() == "Filesystem" {
			obj, err := filesystem.FromSource(p.Source)
			if err != nil {
				return nil, err
			}
			fsState, err := obj.ToState()
			if err != nil {
				return nil, err
			}
			// FIXME:(sipsma) not a good place to hardcode mounting this in, same as mounting in resolver args
			st.AddMount("/mnt/.parent", fsState, llb.ForceNoOutput)
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
