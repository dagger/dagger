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
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"validation", validation(missing), true},
		{"wrapped validation", fmt.Errorf("request: %w", validation(missing)), true},
		{"suggestion", validation(missing + ` Did you mean "items"?`), true},
		{"list", gqlerror.List{validation(missing)}, true},
		{"http", &graphql.HTTPError{StatusCode: 400, Response: graphql.Response{Errors: gqlerror.List{validation(missing)}}}, true},
		{"unstructured", errors.New(missing), false},
		{"unclassified", &gqlerror.Error{Message: missing}, false},
		{"resolver already ran", resolver, false},
		{"http resolver", &graphql.HTTPError{StatusCode: 400, Response: graphql.Response{Errors: gqlerror.List{resolver}}}, false},
		{"mixed errors", gqlerror.List{validation(missing), resolver}, false},
		{"other field", validation(`Cannot query field "other" on type "Artifacts".`), false},
		{"network", errors.New("connection closed"), false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isMissingArtifactListingField(tc.err))
		})
	}
}
