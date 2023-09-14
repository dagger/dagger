package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/moduleconfig"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resolver"
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

	// Dependencies as configured by the module
	DependencyConfig []string `json:"dependencyConfig"`

	// The module's functions
	Functions []*Function `json:"functions,omitempty"`

	// (Not in public API) The container used to execute the module's functions,
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

// FromConfig creates a module from a dagger.json config file.
func (mod *Module) FromConfig(
	ctx context.Context,
	bk *buildkit.Client,
	svcs *Services,
	progSock string,
	sourceDir *Directory,
	configPath string,
) (*Module, error) {
	// Read the config file
	configPath = moduleconfig.NormalizeConfigPath(configPath)

	configFile, err := sourceDir.File(ctx, bk, svcs, configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get config file: %w", err)
	}
	configBytes, err := configFile.Contents(ctx, bk, svcs)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var cfg moduleconfig.Config
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Recursively load the configs of all the dependencies
	var eg errgroup.Group
	mod.Dependencies = make([]*Module, len(cfg.Dependencies))
	for i, depURL := range cfg.Dependencies {
		i, depURL := i, depURL
		eg.Go(func() error {
			modRef, err := resolver.ResolveStableRef(depURL)
			if err != nil {
				return fmt.Errorf("failed to parse dependency url %q: %w", depURL, err)
			}

			// TODO: In theory should first load *just* the config file, figure out the include/exclude, and then load everything else
			// based on that. That's not straightforward because we can't get the config file until we've loaded the dep...
			// May need to have `dagger mod use` and `dagger mod sync` automatically include dependency include/exclude filters in
			// dagger.json.
			var depSourceDir *Directory
			var depConfigPath string
			switch {
			case modRef.Local:
				depSourceDir = sourceDir
				depConfigPath = moduleconfig.NormalizeConfigPath(path.Join("/", path.Dir(configPath), modRef.Path))
			case modRef.Git != nil:
				var err error
				depSourceDir, err = NewDirectorySt(ctx, llb.Git(modRef.Git.CloneURL, modRef.Version), "", mod.Pipeline, mod.Platform, nil)
				if err != nil {
					return fmt.Errorf("failed to create git directory: %w", err)
				}
				depConfigPath = moduleconfig.NormalizeConfigPath(modRef.SubPath)
			default:
				return fmt.Errorf("invalid dependency url from %q", depURL)
			}

			depMod, err := NewModule(mod.Platform, mod.Pipeline).FromConfig(ctx, bk, svcs, progSock, depSourceDir, depConfigPath)
			if err != nil {
				return fmt.Errorf("failed to get dependency mod from config %q: %w", depURL, err)
			}
			mod.Dependencies[i] = depMod
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	// Reposition the root of the sourceDir in case it's pointing to a subdir of current sourceDir
	if cfg.Root != "" {
		rootPath := filepath.Join("/", filepath.Dir(configPath), cfg.Root)
		if rootPath != "/" {
			var err error
			sourceDir, err = sourceDir.Directory(ctx, bk, svcs, rootPath)
			if err != nil {
				return nil, fmt.Errorf("failed to get root directory: %w", err)
			}
			configPath = filepath.Join("/", strings.TrimPrefix(configPath, rootPath))
		}
	}

	// fill in the module settings and set the runtime container
	mod.SourceDirectory = sourceDir
	mod.SourceDirectorySubpath = filepath.Dir(configPath)
	mod.Name = cfg.Name
	mod.SDK = cfg.SDK
	mod.DependencyConfig = cfg.Dependencies
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
	// need to clone fn too since updateMod will mutate it
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

// DigestWithoutFunctions gives a digest after unsetting Functions, which is useful
// as a digest of the "base" Module that's stable before+after loading Functions.
func (mod *Module) DigestWithoutFunctions() (digest.Digest, error) {
	mod, err := mod.Clone()
	if err != nil {
		return "", fmt.Errorf("failed to clone module: %w", err)
	}
	mod.Functions = nil
	return stableDigest(mod)
}

/* The code below handles some subtleties around circular references between modules and functions.

The constraints around the problems being solved are:
1. Modules need to reference the Functions they serve.
2. Functions need to know the Module they are from so that they can be called in the context of that Module.
   * This creates headaches in that setting the Module of a Function changes the ID of the Function, which changes the ID of the Module, which goes full-circle and changes the ID of the Function.
3. If a Module is mutated in any way, its Functions need to be updated to reflect that mutation.
   * This includes the CurrentModule API needing to return the correct Module (not the Module at the time the Function was registered)
4. We need to avoid circular references during JSON marshalling
5. We want to support functions from one Module being registered with a different Module.
   * This enables extending one Module with functionality from another.

The solution to these problems is:
1. Modules hold a list of *Function, but Functions hold a ModuleID (avoids circular reference during JSOM marshal)
2. When a Module is serialized to ID, it unsets the ModuleID value of any Functions it contains that point to itself.
   * If there is a Function from a different Module registered as part of this one, it's left alone.
   * This means that the ModuleID set in a Function doesn't need to try to contain a circular reference to the Function.
3. When a Module is deserialized from ID, it sets the ModuleID value of any Functions it contains that point to itself.
   * If there is a Function from a different Module registered as part of this one, it's left alone.
*/

// update existing Functions with the current state of their module
func (mod *Module) updateMod() error {
	modID, err := mod.ID()
	if err != nil {
		return fmt.Errorf("failed to get module ID: %w", err)
	}
	if err := mod.setFunctionMods(modID); err != nil {
		return fmt.Errorf("failed to set function mods: %w", err)
	}
	return nil
}

func (id ModuleID) String() string {
	return string(id)
}

func (id ModuleID) Decode() (*Module, error) {
	// deserialize the module and then fill in the ModuleID of its functions
	mod, err := resourceid.ID[Module](id).Decode()
	if err != nil {
		return nil, err
	}
	if err := mod.setFunctionMods(id); err != nil {
		return nil, fmt.Errorf("failed to set function mods: %w", err)
	}
	return mod, nil
}

func (mod *Module) ID() (ModuleID, error) {
	mod, err := mod.Clone()
	if err != nil {
		return "", fmt.Errorf("failed to clone module: %w", err)
	}
	// unset the ModuleID fields of any of this module's functions and use that to serialize to ID
	if err := mod.setFunctionMods(""); err != nil {
		return "", fmt.Errorf("failed to set function mods: %w", err)
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
		return "", fmt.Errorf("failed to set function mods: %w", err)
	}
	return stableDigest(mod)
}

func (mod *Module) setFunctionMods(id ModuleID) error {
	memo := map[string]struct{}{}
	var set func(parentName string, fn *Function) error
	set = func(parentName string, fn *Function) error {
		memoKey := fmt.Sprintf("%s.%s", parentName, fn.Name)
		if _, ok := memo[memoKey]; ok {
			return nil
		}
		memo[memoKey] = struct{}{}

		// recurse to any subobjects
		if fn.ReturnType.Kind == TypeDefKindObject {
			for _, subFn := range fn.ReturnType.AsObject.Functions {
				if err := set(fn.ReturnType.AsObject.Name, subFn); err != nil {
					return err
				}
			}
		}

		if fn.ModuleID == "" {
			fn.ModuleID = id
			return nil
		}

		// Check to see if this function belongs to this module by comparing the
		// module name. If so, update the function's ModuleID to id.
		fnMod, err := fn.ModuleID.Decode()
		if err != nil {
			return fmt.Errorf("failed to decode module for function %q: %w", fn.Name, err)
		}
		if fnMod.Name == mod.Name {
			fn.ModuleID = id
		}
		return nil
	}

	for _, fn := range mod.Functions {
		if err := set("", fn); err != nil {
			return err
		}
	}
	return nil
}
