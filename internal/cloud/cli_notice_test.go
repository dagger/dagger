package cloud

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOrgCLINotice(t *testing.T) {
	respond := func(t *testing.T, body string) *httptest.Server {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req, _ := io.ReadAll(r.Body)
			require.Contains(t, string(req), "GetOrgCLINotice")
			require.Contains(t, string(req), `"org":"acme"`)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	t.Run("a notice", func(t *testing.T) {
		srv := respond(t, `{"data":{"org":{"cliNotice":{"level":"WARNING","title":"You are on a trial.","body":"Enter payment details."}}}}`)
		notice, err := testClient(t, srv.URL).OrgCLINotice(context.Background(), "acme")
		require.NoError(t, err)
		require.Equal(t, &CLINotice{Level: CLINoticeLevelWarning, Title: "You are on a trial.", Body: "Enter payment details."}, notice)
	})

	t.Run("no notice", func(t *testing.T) {
		srv := respond(t, `{"data":{"org":{"cliNotice":null}}}`)
		notice, err := testClient(t, srv.URL).OrgCLINotice(context.Background(), "acme")
		require.NoError(t, err)
		require.Nil(t, notice)
	})

	t.Run("an API without the field is an error", func(t *testing.T) {
		srv := respond(t, `{"errors":[{"message":"Cannot query field \"cliNotice\" on type \"Org\"."}]}`)
		_, err := testClient(t, srv.URL).OrgCLINotice(context.Background(), "acme")
		require.Error(t, err)
	})
}
