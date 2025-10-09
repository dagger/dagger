package gogenerator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dschmidt/go-layerfs"
	"github.com/iancoleman/strcase"
	"github.com/psanford/memfs"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

func (g *GoGenerator) GenerateModule(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	if g.Config.ModuleConfig == nil {
		return nil, fmt.Errorf("generateModule is called but module config is missing")
	}

	moduleConfig := g.Config.ModuleConfig

	generator.SetSchema(schema)

	// 1. if no go.mod, generate go.mod
	// 2. if no .go files, bootstrap package main
	//  2a. generate dagger.gen.go from base client,
	//  2b. add stub main.go
	// 3. load package, generate dagger.gen.go (possibly again)

	outDir := filepath.Clean(moduleConfig.ModuleSourcePath)

	mfs := memfs.New()

	var overlay fs.FS = layerfs.New(
		mfs,
		&MountedFS{FS: dagger.QueryBuilder, Name: filepath.Join(outDir, "internal")},
		&MountedFS{FS: dagger.Telemetry, Name: filepath.Join(outDir, "internal")},
	)

	genSt := &generator.GeneratedState{
		Overlay: overlay,
	}

	pkgInfo, partial, err := g.bootstrapMod(mfs, genSt)
	if err != nil {
		return nil, fmt.Errorf("bootstrap package: %w", err)
	}
	if outDir != "." {
		mfs.MkdirAll(outDir, 0700)
		fs, err := mfs.Sub(outDir)
		if err != nil {
			return nil, err
		}
		mfs = fs.(*memfs.FS)
	}

	initialGoFiles, err := filepath.Glob(filepath.Join(g.Config.OutputDir, outDir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("glob go files: %w", err)
	}

	genFile := filepath.Join(g.Config.OutputDir, outDir, ClientGenFile)
	if _, err := os.Stat(genFile); err != nil {
		// assume package main, default for modules
		pkgInfo.PackageName = "main"

		// generate an initial dagger.gen.go from the base Dagger API
		if err := generateCode(ctx, g.Config, schema, schemaVersion, mfs, pkgInfo, nil, nil, 0); err != nil {
			return nil, fmt.Errorf("generate code: %w", err)
		}

		partial = true
	}

	if len(initialGoFiles) == 0 {
		// write an initial main.go if no main pkg exists yet
		dependencies := schema.ExtractDependencies()
		moduleSource := generateModuleSource(pkgInfo, moduleConfig.ModuleName, dependencies)
		if err := mfs.WriteFile(StarterTemplateFile, []byte(moduleSource), 0600); err != nil {
			return nil, err
		}

		// main.go is actually an input to codegen, so this requires another pass
		partial = true
	}

	if partial {
		genSt.NeedRegenerate = true
		return genSt, nil
	}

	pkg, fset, err := loadPackage(ctx, filepath.Join(g.Config.OutputDir, outDir), false)
	if err != nil {
		return nil, fmt.Errorf("load package %q: %w", outDir, err)
	}

	// respect existing package name
	pkgInfo.PackageName = pkg.Name

	if err := generateCode(ctx, g.Config, schema, schemaVersion, mfs, pkgInfo, pkg, fset, 1); err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}

	return genSt, nil
}

func (g *GoGenerator) bootstrapMod(mfs *memfs.FS, genSt *generator.GeneratedState) (*PackageInfo, bool, error) {
	moduleConfig := g.Config.ModuleConfig

	var needsRegen bool

	var daggerModPath string
	var goMod *modfile.File

	modname := fmt.Sprintf("dagger/%s", strcase.ToKebab(moduleConfig.ModuleName))
	// check for a go.mod already for the dagger module
	if content, err := os.ReadFile(filepath.Join(g.Config.OutputDir, moduleConfig.ModuleSourcePath, "go.mod")); err == nil {
		daggerModPath = moduleConfig.ModuleSourcePath

		goMod, err = modfile.ParseLax("go.mod", content, nil)
		if err != nil {
			return nil, false, fmt.Errorf("parse go.mod: %w", err)
		}

		if moduleConfig.IsInit && goMod.Module.Mod.Path != modname {
			return nil, false, fmt.Errorf("existing go.mod path %q does not match the module's name %q", goMod.Module.Mod.Path, modname)
		}
	}

	// could not find a go.mod, so we can init a basic one
	if goMod == nil {
		daggerModPath = moduleConfig.ModuleSourcePath
		goMod = new(modfile.File)

		goMod.AddModuleStmt(modname)
		goMod.AddGoStmt(goVersion)

		needsRegen = true
	}

	// sanity check the parsed go version
	//
	// if this fails, then the go.mod version is too high! and in that case, we
	// won't be able to load the resulting package
	if goMod.Go == nil {
		return nil, false, fmt.Errorf("go.mod has no go directive")
	}
	if semver.Compare("v"+goMod.Go.Version, "v"+goVersion) > 0 {
		return nil, false, fmt.Errorf("existing go.mod has unsupported version %v (highest supported version is %v)", goMod.Go.Version, goVersion)
	}

	if err := g.syncModReplaceAndTidy(goMod, genSt, daggerModPath); err != nil {
		return nil, false, err
	}

	// try and find a go.sum next to the go.mod, and use that to pin
	sum, err := os.ReadFile(filepath.Join(g.Config.OutputDir, daggerModPath, "go.sum"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("could not read go.sum: %w", err)
	}
	sum = append(sum, '\n')
	sum = append(sum, dagger.GoSum...)

	modBody, err := goMod.Format()
	if err != nil {
		return nil, false, fmt.Errorf("format go.mod: %w", err)
	}

	if err := mfs.MkdirAll(daggerModPath, 0700); err != nil {
		return nil, false, err
	}
	if err := mfs.WriteFile(filepath.Join(daggerModPath, "go.mod"), modBody, 0600); err != nil {
		return nil, false, err
	}
	if err := mfs.WriteFile(filepath.Join(daggerModPath, "go.sum"), sum, 0600); err != nil {
		return nil, false, err
	}

	packageImport, err := filepath.Rel(daggerModPath, moduleConfig.ModuleSourcePath)
	if err != nil {
		return nil, false, err
	}
	return &PackageInfo{
		// PackageName is unknown until we load the package
		PackageImport: path.Join(goMod.Module.Mod.Path, packageImport),
	}, needsRegen, nil
}

func (g *GoGenerator) syncModReplaceAndTidy(mod *modfile.File, genSt *generator.GeneratedState, modPath string) error {
	modDir := filepath.Join(g.Config.OutputDir, modPath)

	// if there is a go.work, we need to also set overrides there, otherwise
	// modules will have individually conflicting replace directives
	goWork, err := goEnv(modDir, "GOWORK")
	if err != nil {
		return fmt.Errorf("find go.work: %w", err)
	}

	// use Go SDK's embedded go.mod as basis for pinning versions
	sdkMod, err := modfile.Parse("go.mod", dagger.GoMod, nil)
	if err != nil {
		return fmt.Errorf("parse embedded go.mod: %w", err)
	}
	modRequires := make(map[string]*modfile.Require)
	for _, req := range mod.Require {
		modRequires[req.Mod.Path] = req
	}
	for _, minReq := range sdkMod.Require {
		// check if mod already at least this version
		if currentReq, ok := modRequires[minReq.Mod.Path]; ok {
			if semver.Compare(currentReq.Mod.Version, minReq.Mod.Version) >= 0 {
				continue
			}
		}
		modRequires[minReq.Mod.Path] = minReq
		mod.AddNewRequire(minReq.Mod.Path, minReq.Mod.Version, minReq.Indirect)
	}

	// preserve any replace directives in sdk/go's go.mod (e.g. pre-1.0 packages)
	for _, minReq := range sdkMod.Replace {
		if _, ok := modRequires[minReq.New.Path]; !ok {
			// ignore anything that's sdk/go only
			continue
		}
		genSt.PostCommands = append(genSt.PostCommands,
			exec.Command("go", "mod", "edit", "-replace", minReq.Old.Path+"="+minReq.New.Path+"@"+minReq.New.Version))
		if goWork != "" {
			genSt.PostCommands = append(genSt.PostCommands,
				exec.Command("go", "work", "edit", "-replace", minReq.Old.Path+"="+minReq.New.Path+"@"+minReq.New.Version))
		}
	}

	genSt.PostCommands = append(genSt.PostCommands,
		// run 'go mod tidy' after generating to fix and prune dependencies
		//
		// NOTE: this has to happen before 'go work use' to synchronize Go version
		// bumps
		exec.Command("go", "mod", "tidy"))

	if goWork != "" {
		// run "go work use ." after generating if we had a go.work at the root
		genSt.PostCommands = append(genSt.PostCommands, exec.Command("go", "work", "use", "."))
	}

	return nil
}

// dependencyModule represents a dependency module with its name and functions
// This is an internal struct used during code generation.
type dependencyModule struct {
	moduleName string
	functions  []*introspection.Field
}

// generateModuleSource generates the initial main.go source for a new module.
// If the module has dependencies, it generates passthrough functions for them.
// Otherwise, it generates the default starter functions (containerEcho and grepDir).
func generateModuleSource(pkgInfo *PackageInfo, moduleName string, dependencies []*introspection.DependencyModule) string {
	if len(dependencies) > 0 {
		// Convert DependencyModule to internal dependencyModule format
		var depModules []dependencyModule
		for _, dep := range dependencies {
			depModules = append(depModules, dependencyModule{
				moduleName: dep.Name,
				functions:  dep.Functions,
			})
		}
		if len(depModules) > 0 {
			return generatePassthroughModuleSource(pkgInfo, moduleName, depModules)
		}
	}

	// No dependencies, generate default starter module
	return baseModuleSource(pkgInfo, moduleName)
}

// generatePassthroughModuleSource generates a module with passthrough functions
// for each function in the dependency modules.
func generatePassthroughModuleSource(pkgInfo *PackageInfo, moduleName string, dependencies []dependencyModule) string {
	moduleStructName := strcase.ToCamel(moduleName)

	var functionsCode strings.Builder

	// Generate passthrough functions for each dependency module
	for _, dep := range dependencies {
		depName := strcase.ToCamel(dep.moduleName)

		for _, field := range dep.functions {
			// Only generate for non-deprecated functions
			if field.IsDeprecated {
				continue
			}

			// Get function signature
			funcName := strcase.ToCamel(field.Name)

			// Dont generate the internal Id function
			if funcName == "Id" {
				continue
			}

			// Build parameter list (only required args)
			var params []string
			var callArgs []string
			needsContext := false

			for _, arg := range field.Args {
				// Skip optional arguments
				if arg.TypeRef.IsOptional() {
					continue
				}

				argType := formatGoType(arg.TypeRef)
				argName := strcase.ToLowerCamel(arg.Name)
				params = append(params, fmt.Sprintf("%s %s", argName, argType))
				callArgs = append(callArgs, argName)

				// Check if we need context
				if needsContextForType(arg.TypeRef) {
					needsContext = true
				}
			}

			// Check if return type needs context
			if needsContextForType(field.TypeRef) {
				needsContext = true
			}

			// Add context parameter if needed
			if needsContext {
				params = append([]string{"ctx context.Context"}, params...)
				callArgs = append([]string{"ctx"}, callArgs...)
			}

			// Format return type
			returnType := formatGoType(field.TypeRef)
			hasError := returnsError(field.TypeRef)

			// Adjust return type for error handling
			if hasError && !strings.Contains(returnType, "error") {
				returnType = fmt.Sprintf("(%s, error)", returnType)
			}

			// Build function
			if field.Description != "" {
				functionsCode.WriteString(fmt.Sprintf("\n// %s\n", strings.TrimSpace(field.Description)))
			} else {
				functionsCode.WriteString(fmt.Sprintf("\n// %s calls the %s function from the %s dependency\n", funcName, field.Name, dep.moduleName))
			}
			functionsCode.WriteString(fmt.Sprintf("func (m *%s) %s(%s) %s {\n", moduleStructName, funcName, strings.Join(params, ", "), returnType))

			// Build the function call to the dependency
			functionCall := fmt.Sprintf("dag.%s().%s(%s)", depName, funcName, strings.Join(callArgs, ", "))

			// Handle return
			if returnsObject(field.TypeRef) {
				// Object return type - just return directly
				functionsCode.WriteString(fmt.Sprintf("\treturn %s\n", functionCall))
			} else if hasError {
				// Scalar return type that needs execution - add context and error handling
				functionsCode.WriteString(fmt.Sprintf("\treturn %s\n", functionCall))
			} else {
				// Simple return
				functionsCode.WriteString(fmt.Sprintf("\treturn %s\n", functionCall))
			}

			functionsCode.WriteString("}\n")
		}
	}

	return fmt.Sprintf(`// A generated module for %[1]s functions
//
// This module has been generated from a blueprint and provides passthrough
// functions to the original module.

package main

import (
	"context"
	"%[2]s/internal/dagger"
)

type %[1]s struct{}
%[3]s
`, moduleStructName, pkgInfo.PackageImport, functionsCode.String())
}

// returnsError checks if a GraphQL field requires error handling in Go
func returnsError(typeRef *introspection.TypeRef) bool {
	if typeRef == nil {
		return false
	}

	// Unwrap non-null and list
	actualType := typeRef
	for actualType.Kind == introspection.TypeKindNonNull || actualType.Kind == introspection.TypeKindList {
		if actualType.OfType == nil {
			break
		}
		actualType = actualType.OfType
	}

	// Scalar types generally need error handling for execution
	return actualType.Kind == introspection.TypeKindScalar
}

// returnsObject checks if a type returns an object (which doesn't need immediate execution)
func returnsObject(typeRef *introspection.TypeRef) bool {
	if typeRef == nil {
		return false
	}

	// Unwrap non-null and list
	actualType := typeRef
	for actualType.Kind == introspection.TypeKindNonNull || actualType.Kind == introspection.TypeKindList {
		if actualType.OfType == nil {
			break
		}
		actualType = actualType.OfType
	}

	return actualType.Kind == introspection.TypeKindObject
}

// formatGoType converts an introspection TypeRef to a Go type string
func formatGoType(typeRef *introspection.TypeRef) string {
	if typeRef == nil {
		return "interface{}"
	}

	switch typeRef.Kind {
	case introspection.TypeKindNonNull:
		// For non-null, just recurse
		return formatGoType(typeRef.OfType)
	case introspection.TypeKindList:
		return "[]" + formatGoType(typeRef.OfType)
	case introspection.TypeKindScalar:
		return mapScalarType(typeRef.Name)
	case introspection.TypeKindEnum:
		return mapEnumType(typeRef.Name)
	case introspection.TypeKindObject:
		return "*dagger." + typeRef.Name
	default:
		return "interface{}"
	}
}

// mapScalarType maps GraphQL scalar types to Go types
func mapScalarType(name string) string {
	switch name {
	case "String":
		return "string"
	case "Int":
		return "int"
	case "Float":
		return "float64"
	case "Boolean":
		return "bool"
	case "ID":
		return "string"
	default:
		return name
	}
}

// mapEnumType maps GraphQL enum types to Go types
func mapEnumType(name string) string {
	// Enums are typically generated as strings in the dagger package
	return name
}

// needsContextForType checks if a type requires a context parameter
func needsContextForType(typeRef *introspection.TypeRef) bool {
	// For now, we'll assume any function that returns a scalar or requires
	// async operations needs context. This is a simplification.
	if typeRef == nil {
		return false
	}

	// Unwrap non-null and list
	actualType := typeRef
	for actualType.Kind == introspection.TypeKindNonNull || actualType.Kind == introspection.TypeKindList {
		if actualType.OfType == nil {
			break
		}
		actualType = actualType.OfType
	}

	// Check if it's a scalar type (which might need context for execution)
	return actualType.Kind == introspection.TypeKindScalar
}

func baseModuleSource(pkgInfo *PackageInfo, moduleName string) string {
	moduleStructName := strcase.ToCamel(moduleName)

	return fmt.Sprintf(`// A generated module for %[1]s functions
//
// This module has been generated via dagger init and serves as a reference to
// basic module structure as you get started with Dagger.
//
// Two functions have been pre-created. You can modify, delete, or add to them,
// as needed. They demonstrate usage of arguments and return types using simple
// echo and grep commands. The functions can be called from the dagger CLI or
// from one of the SDKs.
//
// The first line in this comment block is a short description line and the
// rest is a long description with more detail on the module's purpose or usage,
// if appropriate. All modules should have a short description.

package main

import (
	"context"
	"%[2]s/internal/dagger"
)

type %[1]s struct{}

// Returns a container that echoes whatever string argument is provided
func (m *%[1]s) ContainerEcho(stringArg string) *dagger.Container {
	return dag.Container().From("alpine:latest").WithExec([]string{"echo", stringArg})
}

// Returns lines that match a pattern in the files of the provided Directory
func (m *%[1]s) GrepDir(ctx context.Context, directoryArg *dagger.Directory, pattern string) (string, error) {
	return dag.Container().
		From("alpine:latest").
		WithMountedDirectory("/mnt", directoryArg).
		WithWorkdir("/mnt").
		WithExec([]string{"grep", "-R", pattern, "."}).
		Stdout(ctx)
}
`, moduleStructName, pkgInfo.PackageImport)
}

func goEnv(dir string, env string) (string, error) {
	buf := new(bytes.Buffer)
	findGoWork := exec.Command("go", "env", env)
	findGoWork.Dir = dir
	findGoWork.Stdout = buf
	findGoWork.Stderr = os.Stderr
	if err := findGoWork.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}
