package daggercmd

import (
	"slices"
	"sync"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/naming"
)

// The CLI spells the names it derives from the schema (commands, flags,
// type names in addresses) by the naming rules of the engine version it
// ships with: a client follows the version it connects with (see
// hack/designs/identifier-casing.md). Per module would match the engine's
// own CLI paths (check and generator names) more closely, but the typedefs
// the CLI loads don't say which engine version each module declares.
//
// What a user types is matched by the legacy (strcase) rules too, so the
// spellings older CLIs printed (--e-2-e-test for an e2eTest argument) keep
// working.

// cliDictionary is the naming dictionary of this CLI's engine version.
var cliDictionary = naming.DictionaryFor(engine.Version)

func formatCLI(name string, casing naming.Casing, legacy func(string) string) string {
	id, err := cliDictionary.Parse(name)
	if err != nil {
		return legacy(name)
	}
	return id.Format(casing, naming.Uppercase)
}

// gqlObjectName converts casing to a GraphQL object name.
func gqlObjectName(name string) string {
	return formatCLI(name, naming.Pascal, strcase.ToCamel)
}

// gqlFieldName converts casing to a GraphQL field name.
func gqlFieldName(name string) string {
	return formatCLI(name, naming.Camel, strcase.ToLowerCamel)
}

// cliName converts casing to the CLI convention (kebab).
func cliName(name string) string {
	return formatCLI(name, naming.Kebab, strcase.ToKebab)
}

// legacyCLIName is cliName by the legacy strcase rules, which name the
// fields of modules older than engine v1.0.0.
func legacyCLIName(name string) string {
	return strcase.ToKebab(name)
}

// sameObjectName reports whether two names normalize to the same GraphQL
// object name by either the current or the legacy rules.
func sameObjectName(a, b string) bool {
	return a == b ||
		gqlObjectName(a) == gqlObjectName(b) ||
		strcase.ToCamel(a) == strcase.ToCamel(b)
}

// fieldNameCandidates are the field names a module may have given name: by
// the current rules, then by the legacy ones when they differ.
func fieldNameCandidates(name string) []string {
	latest, legacy := gqlFieldName(name), strcase.ToLowerCamel(name)
	if latest == legacy {
		return []string{latest}
	}
	return []string{latest, legacy}
}

// legacyFlagNames maps the legacy CLI spelling of each function argument's
// flag to its current one, where they differ (e-2-e-test to e2e-test), so
// normalizeFlagName accepts both. It's filled as flags are named.
var legacyFlagNames sync.Map

// registerFlagName records the legacy spelling of the flag for an argument
// with the given schema name, and returns the flag's name.
func registerFlagName(argName string) string {
	flagName := cliName(argName)
	if legacy := legacyCLIName(argName); legacy != flagName {
		legacyFlagNames.LoadOrStore(legacy, flagName)
	}
	return flagName
}

// normalizeFlagName is the flag normalization of function commands: any
// casing of an argument's name, by the current or the legacy rules, names
// its flag. A name already in lowercase kebab-case is left alone, so the
// CLI's own flags keep their spelling.
func normalizeFlagName(name string) string {
	if flagName, ok := legacyFlagNames.Load(legacyCLIName(name)); ok {
		return flagName.(string)
	}
	if isKebab(name) {
		return name
	}
	return cliName(name)
}

func isKebab(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !('a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// containsFieldName reports whether names contains the field a module may
// have given name, by the current or the legacy rules.
func containsFieldName(names []string, name string) bool {
	for _, candidate := range fieldNameCandidates(name) {
		if slices.Contains(names, candidate) {
			return true
		}
	}
	return false
}
