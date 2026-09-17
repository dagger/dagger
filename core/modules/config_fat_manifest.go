package modules

import (
	"fmt"

	toml "github.com/pelletier/go-toml"
)

// Fat manifest support is transitional. It lives in this file so that it can be
// removed in one piece.
//
// A fat manifest carries a version 2 entrypoint table and the pre-v2 fields at
// the same time, so one file loads on an engine that supports entrypoints and
// on an engine that does not. An engine that predates entrypoints ignores the
// entrypoint table and reads the pre-v2 fields.
//
// Manifest version 2 has no dependency list, and the SDKs still need one. A
// module that declares dependencies therefore stays on the pre-v2 format even
// when it also declares an entrypoint.
//
// To remove fat manifest support once manifest version 2 covers dependencies:
// delete this file, select version 2 in parseCurrentModuleConfigTOML with
// tree.Has("entrypoint") alone, and drop the legacyModuleManifestKeys branch
// from validateModuleManifestV2TOML.

// moduleManifestFormat is the field layout that reads a dagger-module.toml.
type moduleManifestFormat int

const (
	// moduleManifestFormatPreV2 is the transitional pre-v2 TOML schema.
	moduleManifestFormatPreV2 moduleManifestFormat = iota
	// moduleManifestFormatV2 is the entrypoint schema.
	moduleManifestFormatV2
)

// selectModuleManifestFormat decides which format reads a dagger-module.toml:
//
//	no entrypoint                        -> pre-v2
//	entrypoint, no dependencies          -> version 2, pre-v2 fields ignored
//	entrypoint, dependencies, runtime    -> pre-v2, entrypoint ignored
//	entrypoint, dependencies, no runtime -> error, neither format can load it
func selectModuleManifestFormat(tree *toml.Tree) (moduleManifestFormat, error) {
	if !tree.Has("entrypoint") {
		return moduleManifestFormatPreV2, nil
	}
	if !tree.Has("dependencies") {
		return moduleManifestFormatV2, nil
	}
	if tree.Has("runtime") {
		return moduleManifestFormatPreV2, nil
	}
	return moduleManifestFormatPreV2, fmt.Errorf(
		"%s sets %q and %q without %q: manifest version 2 has no dependency list, so a module with dependencies needs a runtime to load",
		Filename, "entrypoint", "dependencies", "runtime",
	)
}

// legacyModuleManifestKeys are the pre-v2 top-level keys that a fat manifest
// carries for older engines. A version 2 read ignores them rather than
// rejecting them.
//
// "dependencies" is absent on purpose: selectModuleManifestFormat never picks
// version 2 for a manifest that has one, so a dependency list reaching the
// version 2 validator is a bug and stays an error.
var legacyModuleManifestKeys = []string{
	"$schema",
	"clients",
	"codegen",
	"disableDefaultFunctionCaching",
	"engineVersion",
	"exclude",
	"include",
	"runtime",
	"source",
}
