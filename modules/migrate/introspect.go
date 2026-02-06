package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/migrate/internal/dagger"
)

// aliasEntry represents a function alias for the config.toml [aliases] section.
type aliasEntry struct {
	FunctionName string
	ModuleName   string
}

// constructorArg represents a constructor argument for a toolchain module.
type constructorArg struct {
	Name         string
	TypeName     string
	IsOptional   bool
	DefaultValue string
}

// introspectProjectModule loads the project module and enumerates its functions
// to generate alias entries.
func introspectProjectModule(ctx context.Context, fullSource *dagger.Directory, cfg *LegacyConfig) ([]aliasEntry, error) {
	mod := fullSource.AsModule(dagger.DirectoryAsModuleOpts{
		SourceRootPath: cfg.Source,
	})

	funcNames, err := mainObjectFunctions(ctx, mod, cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("introspecting project module %q: %w", cfg.Name, err)
	}

	var aliases []aliasEntry
	for _, name := range funcNames {
		aliases = append(aliases, aliasEntry{
			FunctionName: name,
			ModuleName:   cfg.Name,
		})
	}
	return aliases, nil
}

// introspectToolchainConstructors loads each toolchain module and enumerates
// its constructor arguments to generate commented-out config entries.
func introspectToolchainConstructors(ctx context.Context, fullSource *dagger.Directory, toolchains []*LegacyDependency) (map[string][]constructorArg, error) {
	result := make(map[string][]constructorArg)
	for _, tc := range toolchains {
		mod := fullSource.AsModule(dagger.DirectoryAsModuleOpts{
			SourceRootPath: tc.Source,
		})
		args, err := mainObjectConstructorArgs(ctx, mod, tc.Name)
		if err != nil {
			fmt.Printf("WARNING: could not introspect constructor for toolchain %q: %v\n", tc.Name, err)
			continue
		}
		if len(args) > 0 {
			result[tc.Name] = args
		}
	}
	return result, nil
}

// mainObjectFunctions returns the function names of a module's main object.
func mainObjectFunctions(ctx context.Context, mod *dagger.Module, moduleName string) ([]string, error) {
	objects, err := mod.Objects(ctx)
	if err != nil {
		return nil, err
	}

	mainObj, err := findMainObject(ctx, objects, moduleName)
	if err != nil {
		return nil, err
	}

	functions, err := mainObj.Functions(ctx)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, fn := range functions {
		name, err := fn.Name(ctx)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

// mainObjectConstructorArgs returns the constructor arguments of a module's main object.
func mainObjectConstructorArgs(ctx context.Context, mod *dagger.Module, moduleName string) ([]constructorArg, error) {
	objects, err := mod.Objects(ctx)
	if err != nil {
		return nil, err
	}

	mainObj, err := findMainObject(ctx, objects, moduleName)
	if err != nil {
		return nil, err
	}

	constructor := mainObj.Constructor()
	args, err := constructor.Args(ctx)
	if err != nil {
		return nil, err
	}

	var result []constructorArg
	for _, arg := range args {
		name, err := arg.Name(ctx)
		if err != nil {
			return nil, err
		}
		typeDef := arg.TypeDef()
		typeName, err := typeDefName(ctx, typeDef)
		if err != nil {
			typeName = "unknown"
		}
		optional, _ := typeDef.Optional(ctx)
		defaultVal, _ := arg.DefaultValue(ctx)
		result = append(result, constructorArg{
			Name:         name,
			TypeName:     typeName,
			IsOptional:   optional,
			DefaultValue: string(defaultVal),
		})
	}
	return result, nil
}

// findMainObject finds the main object in a module's type list.
// The main object is the one whose source module name matches the module name.
func findMainObject(ctx context.Context, objects []dagger.TypeDef, moduleName string) (*dagger.ObjectTypeDef, error) {
	for _, obj := range objects {
		kind, err := obj.Kind(ctx)
		if err != nil {
			continue
		}
		if kind != dagger.TypeDefKindObjectKind {
			continue
		}
		objDef := obj.AsObject()
		srcMod, err := objDef.SourceModuleName(ctx)
		if err != nil {
			continue
		}
		if strings.EqualFold(srcMod, moduleName) {
			return objDef, nil
		}
	}
	return nil, fmt.Errorf("main object not found for module %q", moduleName)
}

// typeDefName returns a human-readable name for a TypeDef.
func typeDefName(ctx context.Context, td *dagger.TypeDef) (string, error) {
	kind, err := td.Kind(ctx)
	if err != nil {
		return "", err
	}
	switch kind {
	case dagger.TypeDefKindStringKind:
		return "string", nil
	case dagger.TypeDefKindIntegerKind:
		return "int", nil
	case dagger.TypeDefKindBooleanKind:
		return "bool", nil
	case dagger.TypeDefKindObjectKind:
		name, err := td.AsObject().Name(ctx)
		if err != nil {
			return "", err
		}
		return name, nil
	case dagger.TypeDefKindListKind:
		elem := td.AsList().ElementTypeDef()
		elemName, err := typeDefName(ctx, elem)
		if err != nil {
			return "[]unknown", nil
		}
		return "[]" + elemName, nil
	default:
		return string(kind), nil
	}
}
