package sdk

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/sdk/sdkmeta"
	"github.com/dagger/dagger/dagql"
)

// LoadBuiltinSDKModuleSource resolves and validates the module source embedded in a
// built-in SDK content manifest. Workspace installation uses this instead of treating
// a built-in runtime name as an ambient local path.
func LoadBuiltinSDKModuleSource(
	ctx context.Context,
	dag *dagql.Server,
	source string,
) (dagql.ObjectResult[*core.ModuleSource], error) {
	var moduleSource dagql.ObjectResult[*core.ModuleSource]
	if source != sdkmeta.Rust {
		return moduleSource, fmt.Errorf("builtin SDK module source %q is not packaged", source)
	}
	manifestDigest, err := rustSDKManifestDigest()
	if err != nil {
		return moduleSource, err
	}
	if _, err := rustSDKDescriptorDigest(); err != nil {
		return moduleSource, err
	}

	var fullSDKDir dagql.ObjectResult[*core.Directory]
	if err := dag.Select(ctx, dag.Root(), &fullSDKDir,
		dagql.Selector{
			Field: "_builtinContainer",
			Args: []dagql.NamedInput{{
				Name:  "digest",
				Value: dagql.String(manifestDigest.String()),
			}},
		},
		dagql.Selector{Field: "rootfs"},
	); err != nil {
		return moduleSource, fmt.Errorf("rust SDK provenance: import packaged content: %w", err)
	}

	if err := dag.Select(ctx, fullSDKDir, &moduleSource,
		dagql.Selector{
			Field: "directory",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String("runtime")}},
		},
		dagql.Selector{Field: "asModuleSource"},
	); err != nil {
		return moduleSource, fmt.Errorf("rust SDK provenance: load packaged runtime module source: %w", err)
	}
	if moduleSource.Self() == nil || !moduleSource.Self().ConfigExists {
		return dagql.ObjectResult[*core.ModuleSource]{}, fmt.Errorf("rust SDK provenance: packaged runtime is not an initialized module")
	}

	// Installation must prove the persisted bare source still resolves to an AsModule
	// result; merely finding a config file would admit an unusable SDK installation.
	var module dagql.ObjectResult[*core.Module]
	if err := dag.Select(ctx, moduleSource, &module, dagql.Selector{
		Field: "asModule",
		Args: []dagql.NamedInput{{
			Name:  "forceDefaultFunctionCaching",
			Value: dagql.Opt(dagql.Boolean(true)),
		}},
	}); err != nil {
		return dagql.ObjectResult[*core.ModuleSource]{}, fmt.Errorf("rust SDK provenance: packaged runtime is not loadable as a module: %w", err)
	}
	return moduleSource, nil
}
