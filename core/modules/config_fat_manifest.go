package modules

import (
	"strings"

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
// Manifest version 2 lists dependencies only as an entrypoint's own, for a
// module without a runtime. A module that declares a runtime and dependencies
// therefore stays on the pre-v2 format even when it also declares an
// entrypoint, so an engine that predates entrypoints still loads it.
//
// To remove fat manifest support once no engine needs the pre-v2 fields:
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
//	entrypoint, dependencies, no runtime -> version 2, with the dependencies
func selectModuleManifestFormat(tree *toml.Tree) moduleManifestFormat {
	if !tree.Has("entrypoint") {
		return moduleManifestFormatPreV2
	}
	if !tree.Has("dependencies") {
		return moduleManifestFormatV2
	}
	if tree.Has("runtime") {
		return moduleManifestFormatPreV2
	}
	// The entrypoint's own dependencies, such as dagger/go, which a Go
	// module's generated entrypoint builds the module with.
	return moduleManifestFormatV2
}

// legacyModuleManifestKeys are the pre-v2 top-level keys that a fat manifest
// carries for older engines. A version 2 read ignores them rather than
// rejecting them.
//
// "dependencies" is absent: a version 2 manifest reads it as the
// entrypoint's dependencies, when it has no runtime. "include" is absent too:
// a version 2 manifest reads it with the same meaning, so a fat manifest's
// include applies on either engine, together with its pre-v2 "exclude" (see
// fatManifestExclusions). A version 2 manifest itself writes an exclusion as a
// "!" pattern in include.
var legacyModuleManifestKeys = []string{
	"$schema",
	"clients",
	"codegen",
	"disableDefaultFunctionCaching",
	"engineVersion",
	"exclude",
	"runtime",
	"source",
}

// fatManifestExclusions returns a fat manifest's pre-v2 exclude as include
// patterns, as the pre-v2 format reads it, so the include that both formats
// read selects the same files on either engine.
func fatManifestExclusions(tree *toml.Tree) []string {
	values, _ := tree.Get("exclude").([]any)
	var include []string
	for _, value := range values {
		exclude, _ := value.(string)
		switch {
		case exclude == "":
		case strings.HasPrefix(exclude, "!"):
			include = append(include, exclude[1:])
		default:
			include = append(include, "!"+exclude)
		}
	}
	return include
}

// hasPreV2Fields reports whether a manifest carries pre-v2 fields beside its
// entrypoint, which makes it a fat manifest. Its include then belongs to the
// pre-v2 format as well, so it doesn't replace the entrypoint's sources
// member, whose files that format's include never had to list.
func hasPreV2Fields(tree *toml.Tree) bool {
	for _, key := range legacyModuleManifestKeys {
		if key != "$schema" && tree.Has(key) {
			return true
		}
	}
	return false
}
