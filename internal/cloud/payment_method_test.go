package cloud

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOrgHasPaymentMethod(t *testing.T) {
	respond := func(t *testing.T, body string) *httptest.Server {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req, _ := io.ReadAll(r.Body)
			require.Contains(t, string(req), "hasPaymentMethod")
			require.Contains(t, string(req), `"org":"acme"`)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	for _, want := range []bool{true, false} {
		srv := respond(t, `{"data":{"org":{"hasPaymentMethod":`+map[bool]string{true: "true", false: "false"}[want]+`}}}`)
		got, err := testClient(t, srv.URL).OrgHasPaymentMethod(context.Background(), "acme")
		require.NoError(t, err)
		require.Equal(t, want, got)
	}

	t.Run("an API without the field is an error", func(t *testing.T) {
		srv := respond(t, `{"errors":[{"message":"Cannot query field \"hasPaymentMethod\" on type \"Org\"."}]}`)
		_, err := testClient(t, srv.URL).OrgHasPaymentMethod(context.Background(), "acme")
		require.Error(t, err)
	})
}
