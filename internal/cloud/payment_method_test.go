package cloud

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOrgPaymentStatus(t *testing.T) {
	respond := func(t *testing.T, body string) *httptest.Server {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req, _ := io.ReadAll(r.Body)
			require.Contains(t, string(req), "GetOrgPaymentStatus")
			require.Contains(t, string(req), "hasPaymentMethod")
			require.Contains(t, string(req), `"org":"acme"`)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	srv := respond(t, `{"data":{"org":{"subscription":{"status":"in_trial","trialEnd":"2026-10-05T20:37:46Z","hasPaymentMethod":false}}}}`)
	status, err := testClient(t, srv.URL).OrgPaymentStatus(context.Background(), "acme")
	require.NoError(t, err)
	require.Equal(t, "in_trial", status.Status)
	require.NotNil(t, status.TrialEnd)
	require.Equal(t, "2026-10-05T20:37:46Z", *status.TrialEnd)
	require.False(t, status.HasPaymentMethod)

	srv = respond(t, `{"data":{"org":{"subscription":{"status":"in_trial","trialEnd":null,"hasPaymentMethod":true}}}}`)
	status, err = testClient(t, srv.URL).OrgPaymentStatus(context.Background(), "acme")
	require.NoError(t, err)
	require.True(t, status.HasPaymentMethod)

	t.Run("an API without the field is an error", func(t *testing.T) {
		srv := respond(t, `{"errors":[{"message":"Cannot query field \"hasPaymentMethod\" on type \"SubscriptionInfo\"."}]}`)
		_, err := testClient(t, srv.URL).OrgPaymentStatus(context.Background(), "acme")
		require.Error(t, err)
	})
}
