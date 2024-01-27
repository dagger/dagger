package core

import (
	"fmt"
	"strings"

	"github.com/iancoleman/strcase"
)

/*
This formats comments in the schema as:
"""
comment
"""

Which avoids corner cases where the comment ends in a `"`.
*/
func formatGqlDescription(desc string, args ...any) string {
	if desc == "" {
		return ""
	}
	return "\n" + strings.TrimSpace(fmt.Sprintf(desc, args...)) + "\n"
}

func gqlObjectName(name string) string {
	// gql object name is capitalized camel case
	return strcase.ToCamel(name)
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
