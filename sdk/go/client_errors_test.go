package dagger

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestGraphQLErrorsPreserveMessagesAndStatus(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		for _, exec := range []bool{false, true} {
			t.Run(fmt.Sprintf("status=%d/exec=%t", status, exec), func(t *testing.T) {
				message := "loading workspace: module \"legacy\" needs migration\n\nTry: dagger -W ."
				extensions := map[string]any{"code": "WORKSPACE_ERROR"}
				if exec {
					extensions = map[string]any{
						"_type": "EXEC_ERROR", "exitCode": float64(17),
						"cmd": []any{"false"}, "stdout": "out\n", "stderr": "err\n",
					}
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					json.NewEncoder(w).Encode(graphql.Response{
						Errors: gqlerror.List{&gqlerror.Error{Message: message, Extensions: extensions}},
					})
				}))
				defer server.Close()

				client := errorWrappedClient{graphql.NewClient(server.URL, server.Client())}
				err := client.MakeRequest(t.Context(), &graphql.Request{Query: `{__typename}`}, &graphql.Response{})
				require.EqualError(t, err, message)
				var gqlErr *gqlerror.Error
				require.ErrorAs(t, err, &gqlErr)
				require.Equal(t, message, gqlErr.Message)
				var extended extendedError
				require.ErrorAs(t, err, &extended)
				require.Equal(t, extensions, extended.Extensions())
				if status != http.StatusOK {
					var httpErr *graphql.HTTPError
					require.ErrorAs(t, err, &httpErr)
					require.Equal(t, status, httpErr.StatusCode)
				}
				if exec {
					var execErr *ExecError
					require.ErrorAs(t, err, &execErr)
					require.Equal(t, 17, execErr.ExitCode)
					require.Equal(t, []string{"false"}, execErr.Cmd)
					require.Equal(t, "out\n", execErr.Stdout)
					require.Equal(t, "err\n", execErr.Stderr)
				}
			})
		}
	}
}

func TestHTTPErrorWithoutGraphQLErrorsIsNotClassified(t *testing.T) {
	err := &graphql.HTTPError{StatusCode: http.StatusServiceUnavailable}
	require.Nil(t, getCustomError(err))
}
