package daggercmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestMissingArtifactListingFieldOnlyMatchesValidation(t *testing.T) {
	missing := `Cannot query field "__listing" on type "Artifacts".`
	validation := func(message string) *gqlerror.Error {
		return &gqlerror.Error{Message: message, Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}
	}
	resolver := validation(missing)
	resolver.Path = ast.Path{ast.PathName("node"), ast.PathName("listing")}
	response := func(errs ...*gqlerror.Error) error {
		return fmt.Errorf("request: %w", &graphql.HTTPError{StatusCode: 422, Response: graphql.Response{Errors: errs}})
	}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"validation", response(validation(missing)), true},
		{"suggestion", response(validation(missing + ` Did you mean "items"?`)), true},
		{"unclassified", response(&gqlerror.Error{Message: missing}), false},
		{"resolver already ran", response(resolver), false},
		{"mixed errors", response(validation(missing), resolver), false},
		{"other field", response(validation(`Cannot query field "other" on type "Artifacts".`)), false},
		{"unstructured", errors.New(missing), false},
		{"network", errors.New("connection closed"), false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isMissingArtifactListingField(tc.err))
		})
	}
}

func TestMissingArtifactSchemaListingOnlyMatchesValidation(t *testing.T) {
	const missing = `Unknown argument "schema" on field "Artifacts.__listing".`
	response := func(message string, path ast.Path, code string) error {
		return &graphql.HTTPError{StatusCode: 422, Response: graphql.Response{Errors: []*gqlerror.Error{{
			Message: message, Path: path, Extensions: map[string]any{"code": code},
		}}}}
	}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"schema validation", response(missing, nil, "GRAPHQL_VALIDATION_FAILED"), true},
		{"older listing field", response(`Cannot query field "__listing" on type "Artifacts".`, nil, "GRAPHQL_VALIDATION_FAILED"), true},
		{"resolver", response(missing, ast.Path{ast.PathName("node")}, "GRAPHQL_VALIDATION_FAILED"), false},
		{"unclassified", response(missing, nil, ""), false},
		{"other argument", response(`Unknown argument "other" on field "Artifacts.__listing".`, nil, "GRAPHQL_VALIDATION_FAILED"), false},
		{"unstructured", errors.New(missing), false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isMissingArtifactSchemaListing(tc.err))
		})
	}
}
