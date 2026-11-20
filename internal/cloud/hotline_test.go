package cloud

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dagger/dagger/internal/cloud/auth"
	"golang.org/x/oauth2"
)

func TestHotlineCloudCredentials(t *testing.T) {
	for _, kind := range []string{"Basic", "Bearer"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "Basic" {
					user, password, ok := r.BasicAuth()
					if !ok || user != "credential" || password != "" {
						t.Error("wrong API credential")
					}
				} else if r.Header.Get("Authorization") != "Bearer credential" {
					t.Error("wrong user credential")
				}
				if r.Header.Get("X-Dagger-Org") != "org-id" || r.Header.Get("Idempotency-Key") != "retry-1" {
					t.Error("missing request scope")
				}
				if r.URL.Path != "/v1/hotline/threads/1" || r.URL.Query().Get("after") != "2" {
					t.Error("wrong path or cursor")
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != "body" {
					t.Error("wrong body")
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			t.Setenv("DAGGER_CLOUD_URL", server.URL)
			client, err := NewClient(context.Background(), &auth.Cloud{Token: &oauth2.Token{AccessToken: "credential", TokenType: kind}})
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Hotline(t.Context(), "org-id", "POST", "/v1/hotline/threads/1?after=2", "retry-1", strings.NewReader("body"))
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal(response.Status)
			}
		})
	}
}

func TestHotlineDoesNotFollowRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			t.Error("followed redirect")
		}
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	t.Setenv("DAGGER_CLOUD_URL", server.URL)
	client, err := NewClient(t.Context(), &auth.Cloud{Token: &oauth2.Token{AccessToken: "credential", TokenType: "Bearer"}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Hotline(t.Context(), "org", "GET", "/v1/hotline/threads", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect {
		t.Fatal(response.Status)
	}
}
