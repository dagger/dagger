package templates

import (
	"strings"

	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/engine/naming"
)

// identifierCasingVersion is engine.IdentifierCasingVersion, the first API
// view whose names follow the GraphQL casing convention. (The engine package
// is too heavy for codegen to import; TestIdentifierCasingVersion keeps them
// in sync.)
const identifierCasingVersion = "v1.0.0-beta.22"

// goNamer formats GraphQL names (types, fields, arguments) as Go
// identifiers. Wire names are never formatted: selections, argument keys,
// input fields and enum values keep the schema's spelling.
type goNamer struct {
	// dict is the naming dictionary of the schema's version, for schemas from
	// engine.IdentifierCasingVersion on: names are parsed into words and
	// written PascalCase with uppercase acronyms (commitSha and commitSHA are
	// both CommitSHA, withGpu is WithGPU). Nil for earlier schemas, whose
	// identifiers keep the golint-style initialisms (lintName) byte for byte.
	dict *naming.Dictionary
}

// newGoNamer returns the namer for a schema of schemaVersion, its
// __schemaVersion.
func newGoNamer(schemaVersion string) goNamer {
	if !usesIdentifierNaming(schemaVersion) {
		return goNamer{}
	}
	return goNamer{dict: naming.DictionaryFor(schemaVersion)}
}

// usesIdentifierNaming reports whether a schema of schemaVersion is at or
// past engine.IdentifierCasingVersion, whose names follow the GraphQL casing
// convention. schemaVersion is a schema's __schemaVersion, its API view
// (engine.APIViewVersion), so it orders by strict semver: every view of a
// v1.0.0 prerelease before the cut is v1.0.0-beta.21, and dev builds have
// their release's view.
func usesIdentifierNaming(schemaVersion string) bool {
	if !semver.IsValid(schemaVersion) {
		return false
	}
	return semver.Compare(schemaVersion, identifierCasingVersion) >= 0
}

// formatName formats a GraphQL name (e.g. object, field, arg) into a Go
// equivalent. Example: `fooId` -> `FooID`
func (n goNamer) formatName(s string) string {
	if n.dict != nil && s != "" && !strings.HasPrefix(s, "_") {
		if id, err := n.dict.Parse(s); err == nil {
			return id.Format(naming.Pascal, naming.Uppercase)
		}
	}
	return legacyFormatName(s)
}

// legacyFormatName formats a GraphQL name into a Go equivalent with
// golint-style initialisms, as for schemas before
// engine.IdentifierCasingVersion. Example: `fooId` -> `FooID`
func legacyFormatName(s string) string {
	if len(s) > 0 {
		s = strings.ToUpper(string(s[0])) + s[1:]
	}
	return lintName(s)
}
