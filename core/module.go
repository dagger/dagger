package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/moduleconfig"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
)

const (
	ModMetaDirPath     = "/.daggermod"
	ModMetaInputPath   = "input.json"
	ModMetaOutputPath  = "output.json"
	ModMetaDepsDirPath = "deps"

	ModSourceDirPath      = "/src"
	runtimeExecutablePath = "/runtime"
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

	// The SDK of the module
	SDK moduleconfig.SDK `json:"sdk"`

	// Dependencies of the module
	Dependencies []*Module `json:"dependencies"`

	// The module's functions
	Functions []*Function `json:"functions,omitempty"`

	// (Not in public API) The container used to execute the module's entrypoint code,
	// derived from the SDK, source directory, and workdir.
	Runtime *Container `json:"runtime,omitempty"`

	// (Not in public API) The module's platform
	Platform ocispecs.Platform `json:"platform,omitempty"`

	// (Not in public API) The pipeline in which the module was created
	Pipeline pipeline.Path `json:"pipeline,omitempty"`
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
	for _, dep := range mod.Dependencies {
		depDefs, err := dep.PBDefinitions()
		if err != nil {
			return nil, err
		}
		defs = append(defs, depDefs...)
	}
	return defs, nil
}

func (mod Module) Clone() (*Module, error) {
	cp := mod
	if mod.SourceDirectory != nil {
		cp.SourceDirectory = mod.SourceDirectory.Clone()
	}
	if mod.Runtime != nil {
		cp.Runtime = mod.Runtime.Clone()
	}
	cp.Dependencies = make([]*Module, len(mod.Dependencies))
	for i, dep := range mod.Dependencies {
		var err error
		cp.Dependencies[i], err = dep.Clone()
		if err != nil {
			return nil, fmt.Errorf("failed to clone dependency %q: %w", dep.Name, err)
		}
	}
	cp.Functions = make([]*Function, len(mod.Functions))
	for i, function := range mod.Functions {
		var err error
		cp.Functions[i], err = function.Clone()
		if err != nil {
			return nil, fmt.Errorf("failed to clone function %q: %w", function.Name, err)
		}
	}
	return &cp, nil
}

func NewModule(platform ocispecs.Platform, pipeline pipeline.Path) *Module {
	return &Module{
		Platform: platform,
		Pipeline: pipeline,
	}
}

func (mod *Module) FromConfig(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	sourceDir *Directory,
	configPath string,
) (*Module, error) {
	configPath = moduleconfig.NormalizeConfigPath(configPath)

	configFile, err := sourceDir.File(ctx, bk, configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get config file: %w", err)
	}
	configBytes, err := configFile.Contents(ctx, bk)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var cfg moduleconfig.Config
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	var eg errgroup.Group
	deps := make([]*Module, len(cfg.Dependencies))
	for i, depURL := range cfg.Dependencies {
		i, depURL := i, depURL
		eg.Go(func() error {
			parsedURL, err := moduleconfig.ParseModuleURL(depURL)
			if err != nil {
				return fmt.Errorf("failed to parse dependency url %q: %w", depURL, err)
			}

			// TODO: In theory should first load *just* the config file, figure out the include/exclude, and then load everything else
			// based on that. That's not straightforward because we can't get the config file until we've loaded the dep...
			// May need to have `dagger mod extend` and `dagger mod sync` automatically include dependency include/exclude filters in
			// dagger.json.
			var depSourceDir *Directory
			var depConfigPath string
			switch {
			case parsedURL.Local != nil:
				depSourceDir = sourceDir
				depConfigPath = filepath.Join("/", filepath.Dir(configPath), parsedURL.Local.ConfigPath)
			case parsedURL.Git != nil:
				var err error
				depSourceDir, err = NewDirectorySt(ctx, llb.Git(parsedURL.Git.Repo, parsedURL.Git.Ref), "", mod.Pipeline, mod.Platform, nil)
				if err != nil {
					return fmt.Errorf("failed to create git directory: %w", err)
				}
				depConfigPath = parsedURL.Git.ConfigPath
			default:
				return fmt.Errorf("invalid dependency url from %q", depURL)
			}

			depMod, err := mod.FromConfig(ctx, bk, progSock, depSourceDir, depConfigPath)
			if err != nil {
				return fmt.Errorf("failed to get dependency mod from config %q: %w", depURL, err)
			}
			deps[i] = depMod
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	if cfg.Root != "" {
		rootPath := filepath.Join("/", filepath.Dir(configPath), cfg.Root)
		if rootPath != "/" {
			var err error
			sourceDir, err = sourceDir.Directory(ctx, bk, rootPath)
			if err != nil {
				return nil, fmt.Errorf("failed to get root directory: %w", err)
			}
			configPath = filepath.Join("/", strings.TrimPrefix(configPath, rootPath))
		}
	}

	if err := mod.recalcRuntime(ctx, bk, progSock); err != nil {
		return nil, fmt.Errorf("failed to set runtime container: %w", err)
	}

	return mod, nil
}

func (mod *Module) WithFunction(fn *Function) (*Module, error) {
	mod, err := mod.Clone()
	if err != nil {
		return nil, fmt.Errorf("failed to clone module: %w", err)
	}
	fn, err = fn.Clone()
	if err != nil {
		return nil, fmt.Errorf("failed to clone function: %w", err)
	}
	mod.Functions = append(mod.Functions, fn)
	return mod, mod.updateMod()
}

// recalculate the definition of the runtime based on the current state of the module
func (mod *Module) recalcRuntime(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
) error {
	var runtime *Container
	var err error
	switch mod.SDK {
	case moduleconfig.SDKGo:
		runtime, err = mod.goRuntime(
			ctx,
			bk,
			progSock,
			mod.SourceDirectory,
			mod.SourceDirectorySubpath,
		)
	case moduleconfig.SDKPython:
		runtime, err = mod.pythonRuntime(
			ctx,
			bk,
			progSock,
			mod.SourceDirectory,
			mod.SourceDirectorySubpath,
		)
	default:
		return fmt.Errorf("unknown sdk %q", mod.SDK)
	}
	if err != nil {
		return fmt.Errorf("failed to get base runtime for sdk %s: %w", mod.SDK, err)
	}

	mod.Runtime = runtime
	return mod.updateMod()
}

// TODO: doc various subtleties below

// update existing entrypoints with the current state of their module
func (mod *Module) updateMod() error {
	modID, err := mod.ID()
	if err != nil {
		return fmt.Errorf("failed to get module ID: %w", err)
	}
	if err := mod.setFunctionMods(modID); err != nil {
		return fmt.Errorf("failed to set entrypoint mods: %w", err)
	}
	return nil
}

func (id ModuleID) Decode() (*Module, error) {
	mod, err := resourceid.ID[Module](id).Decode()
	if err != nil {
		return nil, err
	}
	if err := mod.setFunctionMods(id); err != nil {
		return nil, fmt.Errorf("failed to set entrypoint mods: %w", err)
	}
	return mod, nil
}

func (mod *Module) ID() (ModuleID, error) {
	mod, err := mod.Clone()
	if err != nil {
		return "", fmt.Errorf("failed to clone module: %w", err)
	}
	if err := mod.setFunctionMods(""); err != nil {
		return "", fmt.Errorf("failed to set entrypoint mods: %w", err)
	}
	id, err := resourceid.Encode(mod)
	if err != nil {
		return "", fmt.Errorf("failed to encode module to id: %w", err)
	}
	return ModuleID(id), nil
}

func (mod *Module) Digest() (digest.Digest, error) {
	mod, err := mod.Clone()
	if err != nil {
		return "", fmt.Errorf("failed to clone module: %w", err)
	}
	if err := mod.setFunctionMods(""); err != nil {
		return "", fmt.Errorf("failed to set entrypoint mods: %w", err)
	}
	return stableDigest(mod)
}

func (mod *Module) setFunctionMods(id ModuleID) error {
	// TODO: recurse to functions of other objects in the module
	for _, fn := range mod.Functions {
		if fn.ModuleID == "" {
			fn.ModuleID = id
			continue
		}
		fnMod, err := fn.ModuleID.Decode()
		if err != nil {
			return fmt.Errorf("failed to decode module for function %q: %w", fn.Name, err)
		}
		if fnMod.Name == mod.Name {
			fn.ModuleID = id
		}
	}
	return nil
}
