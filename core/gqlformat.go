package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core/compat"
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

func gqlObjectName(ctx context.Context, name string) string {
	// gql object name is capitalized camel case
	return compat.Strcase(ctx).ToPascal(name)
}

func namespaceObject(
	ctx context.Context,
	objOriginalName string,
	modFinalName string,
	modOriginalName string,
) string {
	objOriginalName = gqlObjectName(ctx, objOriginalName)
	if rest := strings.TrimPrefix(objOriginalName, gqlObjectName(ctx, modOriginalName)); rest != objOriginalName {
		if len(rest) == 0 {
			// Main module object with same original name as module original name, give it
			// the same name as the module's final name
			return gqlObjectName(ctx, modFinalName)
		}
		// we have this case check here to check for a boundary
		// e.g. if objName="Postman" and namespace="Post", then we should still namespace
		// this to "PostPostman" instead of just going for "Postman" (but we should do that
		// if objName="PostMan")
		if 'A' <= rest[0] && rest[0] <= 'Z' {
			// objName has original module name prefixed, just make sure it has the final
			// module name as prefix
			return gqlObjectName(ctx, modFinalName+rest)
		}
	}

	// need to namespace object with final module name
	return gqlObjectName(ctx, modFinalName+"_"+objOriginalName)
}

func gqlFieldName(ctx context.Context, name string) string {
	// gql field name is uncapitalized camel case
	return compat.Strcase(ctx).ToCamel(name)
}

func gqlArgName(ctx context.Context, name string) string {
	// gql arg name is uncapitalized camel case
	return compat.Strcase(ctx).ToCamel(name)
}
