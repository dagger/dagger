package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	ModMetaDirPath     = "/.daggermod"
	ModMetaInputPath   = "input.json"
	ModMetaOutputPath  = "output.json"
	ModMetaDepsDirPath = "deps"
)

type Module struct {
	// The module's source code root directory
	SourceDirectory *Directory `json:"sourceDirectory"`

	// If set, the subdir of the SourceDirectory that contains the module's source code
	SourceDirectorySubpath string `json:"sourceDirectorySubpath"`

	// The name of the module
	Name string `json:"name"`

	// The doc string of the module, if any
	Description string `json:"description"`

	// Dependencies as configured by the module
	DependencyConfig []string `json:"dependencyConfig"`

	// The module's objects
	Objects []*TypeDef `json:"objects,omitempty"`

	// The module's SDK, as set in the module config file
	SDK string `json:"sdk,omitempty"`

	// Below are not in public graphql API

	// The container used to execute the module's functions,
	// derived from the SDK, source directory, and workdir.
	Runtime *Container `json:"runtime,omitempty"`

	// The module's platform
	Platform ocispecs.Platform `json:"platform,omitempty"`

	// The pipeline in which the module was created
	Pipeline pipeline.Path `json:"pipeline,omitempty"`
}

func (mod *Module) ID() (ModuleID, error) {
	return resourceid.Encode(mod)
}

func (mod *Module) Digest() (digest.Digest, error) {
	return stableDigest(mod)
}

// Base gives a digest after unsetting Objects+Runtime, which is useful
// as a digest of the "base" Module that's stable before+after loading TypeDefs
func (mod *Module) BaseDigest() (digest.Digest, error) {
	mod = mod.Clone()
	mod.Objects = nil
	mod.Runtime = nil
	return stableDigest(mod)
}

func (mod *Module) PBDefinitions() ([]*pb.Definition, error) {
	var defs []*pb.Definition
	if mod.SourceDirectory != nil {
		dirDefs, err := mod.SourceDirectory.PBDefinitions()
		if err != nil {
			return nil, err
		}
		defs = append(defs, dirDefs...)
	}
	if mod.Runtime != nil {
		ctrDefs, err := mod.Runtime.PBDefinitions()
		if err != nil {
			return nil, err
		}
		defs = append(defs, ctrDefs...)
	}
	return defs, nil
}

func (mod Module) Clone() *Module {
	cp := mod
	if mod.SourceDirectory != nil {
		cp.SourceDirectory = mod.SourceDirectory.Clone()
	}
	if mod.Runtime != nil {
		cp.Runtime = mod.Runtime.Clone()
	}
	cp.DependencyConfig = cloneSlice(mod.DependencyConfig)
	cp.Objects = make([]*TypeDef, len(mod.Objects))
	for i, def := range mod.Objects {
		cp.Objects[i] = def.Clone()
	}
	return &cp
}

func NewModule(platform ocispecs.Platform, pipeline pipeline.Path) *Module {
	return &Module{
		Platform: platform,
		Pipeline: pipeline,
	}
}

// Load the module config as parsed from the given File
func LoadModuleConfigFromFile(
	ctx context.Context,
	bk *buildkit.Client,
	svcs *Services,
	configFile *File,
) (*modules.Config, error) {
	configBytes, err := configFile.Contents(ctx, bk, svcs)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var cfg modules.Config
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	return &cfg, nil
}

// Load the module config from the module from the given diretory at the given path
func LoadModuleConfig(
	ctx context.Context,
	bk *buildkit.Client,
	svcs *Services,
	sourceDir *Directory,
	configPath string,
) (string, *modules.Config, error) {
	configPath = modules.NormalizeConfigPath(configPath)
	configFile, err := sourceDir.File(ctx, bk, svcs, configPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get config file from path %q: %w", configPath, err)
	}
	cfg, err := LoadModuleConfigFromFile(ctx, bk, svcs, configFile)
	if err != nil {
		return "", nil, fmt.Errorf("failed to load config: %w", err)
	}
	return configPath, cfg, nil
}

// callback for retrieving the runtime container for a module; needs to be callback since only the schema/module.go implementation
// knows how to call modules to get the container
type getRuntimeFunc func(ctx context.Context, mod *Module) (*Container, error)

// FromConfig creates a module from a dagger.json config file.
func (mod *Module) FromConfig(
	ctx context.Context,
	bk *buildkit.Client,
	svcs *Services,
	progSock string,
	sourceDir *Directory,
	configPath string,
	getRuntime getRuntimeFunc,
) (*Module, error) {
	configPath, cfg, err := LoadModuleConfig(ctx, bk, svcs, sourceDir, configPath)
	if err != nil {
		return nil, err
	}

	// Reposition the root of the sourceDir in case it's pointing to a subdir of current sourceDir
	if filepath.Clean(cfg.Root) != "." {
		rootPath := filepath.Join(filepath.Dir(configPath), cfg.Root)
		if rootPath != filepath.Dir(configPath) {
			configPathAbs, err := filepath.Abs(configPath)
			if err != nil {
				return nil, fmt.Errorf("failed to get config absolute path: %w", err)
			}
			rootPathAbs, err := filepath.Abs(rootPath)
			if err != nil {
				return nil, fmt.Errorf("failed to get root absolute path: %w", err)
			}

			configPath, err = filepath.Rel(rootPathAbs, configPathAbs)
			if err != nil {
				return nil, fmt.Errorf("failed to get config relative to root: %w", err)
			}
			if strings.HasPrefix(configPath, "../") {
				// this likely shouldn't happen, a client shouldn't submit a
				// module config that escapes the module root
				return nil, fmt.Errorf("module subpath is not under module root")
			}

			sourceDir, err = sourceDir.Directory(ctx, bk, svcs, rootPath)
			if err != nil {
				return nil, fmt.Errorf("failed to get root directory: %w", err)
			}
		}
	}

	// fill in the module settings and set the runtime container
	mod.SourceDirectory = sourceDir
	mod.SourceDirectorySubpath = filepath.Dir(configPath)
	mod.Name = cfg.Name
	mod.DependencyConfig = cfg.Dependencies
	mod.SDK = cfg.SDK
	mod.Runtime, err = getRuntime(ctx, mod)
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime: %w", err)
	}

	return mod, nil
}

// Load the module from the given module reference. parentSrcDir and parentSrcSubpath are used to resolve local module refs
// if needed (i.e. this is a local dep of another module)
func (mod *Module) FromRef(
	ctx context.Context,
	bk *buildkit.Client,
	svcs *Services,
	progSock string,
	parentSrcDir *Directory, // nil if not being loaded as a dep of another mod
	parentSrcSubpath string, // "" if not being loaded as a dep of another mod
	moduleRefStr string,
	getRuntime getRuntimeFunc,
) (*Module, error) {
	modRef, err := modules.ParseRef(moduleRefStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse dependency url %q: %w", moduleRefStr, err)
	}
	parentSrcSubpath = modules.NormalizeConfigPath(parentSrcSubpath)

	// TODO: In theory should first load *just* the config file, figure out the include/exclude, and then load everything else
	// based on that. That's not straightforward because we can't get the config file until we've loaded the dep...
	// May need to have `dagger mod use` and `dagger mod sync` automatically include dependency include/exclude filters in
	// dagger.json.
	var sourceDir *Directory
	var configPath string
	switch {
	case modRef.Local != nil:
		if parentSrcDir == nil {
			return nil, fmt.Errorf("invalid local module ref is local relative to nil parent %q", moduleRefStr)
		}
		sourceDir = parentSrcDir
		configPath = modules.NormalizeConfigPath(filepath.Join(filepath.Dir(parentSrcSubpath), modRef.Local.Path))

		if strings.HasPrefix(configPath+"/", "../") {
			return nil, fmt.Errorf("local module path %q is not under root", modRef.Local.Path)
		}
	case modRef.Git != nil:
		var err error
		sourceDir, err = NewDirectorySt(ctx, llb.Git(modRef.Git.CloneURL.String(), modRef.Git.Commit), "", mod.Pipeline, mod.Platform, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create git directory: %w", err)
		}
		configPath = modules.NormalizeConfigPath(modRef.Git.Dir)
	default:
		return nil, fmt.Errorf("invalid module ref %q", moduleRefStr)
	}

	return mod.FromConfig(ctx, bk, svcs, progSock, sourceDir, configPath, getRuntime)
}

func (mod *Module) WithObject(def *TypeDef) (*Module, error) {
	mod = mod.Clone()
	if def.AsObject == nil {
		return nil, fmt.Errorf("expected object type def, got %s: %+v", def.Kind, def)
	}
	mod.Objects = append(mod.Objects, def)
	return mod, nil
}
