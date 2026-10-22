package core

import (
	"context"
	"strings"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

/*
This formats comments in the schema as:
"""
comment
"""

Which avoids corner cases where the comment ends in a `"`.
*/
func formatGqlDescription(desc string) string {
	if desc == "" {
		return ""
	}
	return "\n" + strings.TrimSpace(desc) + "\n"
}

// IdentifierNamingVersion is the first engine version whose names are
// normalized by engine/naming: modules that declare it or later in their
// engineVersion. Modules that declare an older engineVersion keep the strcase
// rules they were built against, byte for byte. See
// hack/designs/identifier-casing.md, "The gate".
//
// It's engine.IdentifierCasingVersion, the first release whose view keeps
// its prerelease, so no released engine names modules the new way under an
// older version: a module that declares an earlier v1.0.0 prerelease
// (including v1.0.0-0, which shares their pre-cut view) keeps the strcase
// rules.
//
// SDK generators decide which naming rules to apply by whether the schema
// they generate from has Query.formatIdentifiers, which is installed on the
// same view: the two must line up exactly.
const IdentifierNamingVersion = engine.IdentifierCasingVersion

// IdentifierNamingView is the API views whose names follow engine/naming:
// the views of IdentifierNamingVersion and later. It gates both how modules
// are named and the identifier API (Query.formatIdentifiers and friends).
var IdentifierNamingView dagql.ViewFilter = AfterVersion(IdentifierNamingVersion)

func gqlObjectName(name string) string {
	// gql object name is capitalized camel case
	return strcase.ToCamel(name)
}

// NamespaceObject maps a module-local type name to its namespaced schema name.
// It is the exported entry point for SDK runtimes (e.g. the Dang interpreter)
// that need to resolve a local type reference to the name the engine installed.
func NamespaceObject(objOriginalName, modFinalName, modOriginalName string) string {
	return namespaceObject(objOriginalName, modFinalName, modOriginalName)
}

func namespaceObject(
	objOriginalName string,
	modFinalName string,
	modOriginalName string,
) string {
	objOriginalName = gqlObjectName(objOriginalName)
	if rest := strings.TrimPrefix(objOriginalName, gqlObjectName(modOriginalName)); rest != objOriginalName {
		if len(rest) == 0 {
			// Main module object with same original name as module original name, give it
			// the same name as the module's final name
			return gqlObjectName(modFinalName)
		}
		// we have this case check here to check for a boundary
		// e.g. if objName="Postman" and namespace="Post", then we should still namespace
		// this to "PostPostman" instead of just going for "Postman" (but we should do that
		// if objName="PostMan")
		if 'A' <= rest[0] && rest[0] <= 'Z' {
			// objName has original module name prefixed, just make sure it has the final
			// module name as prefix
			return gqlObjectName(modFinalName + rest)
		}
	}

	// need to namespace object with final module name
	return gqlObjectName(modFinalName + "_" + objOriginalName)
}

func gqlFieldName(name string) string {
	// gql field name is uncapitalized camel case
	return strcase.ToLowerCamel(name)
}

func gqlArgName(name string) string {
	// gql arg name is uncapitalized camel case
	return strcase.ToLowerCamel(name)
}

func gqlEnumMemberName(name string) string {
	if isConventionalGraphQLEnumMemberName(name) {
		return name
	}
	return strcase.ToScreamingSnake(name)
}

func isConventionalGraphQLEnumMemberName(name string) bool {
	if name == "" || strings.HasPrefix(name, "__") {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			continue
		}
		if i > 0 && ((c >= '0' && c <= '9') || c == '_') {
			continue
		}
		return false
	}
	return true
}

// CallerView is the API view of the caller of the current field: the view
// recorded in the call when the field is view-scoped, else the view of the
// current server. Empty when neither is known.
func CallerView(ctx context.Context) call.View {
	if cur := dagql.CurrentCall(ctx); cur != nil && cur.View != "" {
		return cur.View
	}
	if srv := dagql.CurrentDagqlServer(ctx); srv != nil {
		return srv.View
	}
	return ""
}
