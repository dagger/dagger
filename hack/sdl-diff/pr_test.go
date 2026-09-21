package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestReplaceSection(t *testing.T) {
	section := sectionStart + "\nnew\n" + sectionEnd
	for _, tc := range []struct {
		name, body, want string
		fails            bool
	}{
		{"empty", "", section, false},
		{"append", "Human text\n", "Human text\n\n\n" + section, false},
		{"replace", "before\n" + sectionStart + "old" + sectionEnd + "\nafter", "before\n" + section + "\nafter", false},
		{"idempotent", section, section, false},
		{"missing end", sectionStart, "", true},
		{"missing start", sectionEnd, "", true},
		{"reversed", sectionEnd + sectionStart, "", true},
		{"duplicate", section + section, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := replaceSection(tc.body, section)
			if (err != nil) != tc.fails || got != tc.want {
				t.Fatalf("got %q, %v; want %q, error=%v", got, err, tc.want, tc.fails)
			}
		})
	}
}

func TestRenderSection(t *testing.T) {
	empty := renderSection("", "base", "head", false)
	if !strings.Contains(empty, "No semantic API changes.") || !strings.Contains(empty, "go run ./hack/sdl-diff -descriptions=false base:"+schemaPath+" head:"+schemaPath) {
		t.Fatal(empty)
	}
	got := renderSection("# docs with ``` and ````\n", "base", "head", true)
	if !strings.Contains(got, "`````graphql\n") || !strings.Contains(got, "`go run ./hack/sdl-diff base:"+schemaPath+" head:"+schemaPath+"`") {
		t.Fatal(got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPRWorkflow(t *testing.T) {
	base, head, merge := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	for _, tc := range []struct {
		name                                                             string
		publish, changed, closed, noOp, invalid, oversized, descriptions bool
	}{
		{name: "preview"},
		{name: "publish", publish: true},
		{name: "concurrent edit", publish: true, changed: true},
		{name: "closed", publish: true, closed: true},
		{name: "already current", publish: true, noOp: true},
		{name: "invalid markers", publish: true, invalid: true},
		{name: "oversized", publish: true, oversized: true},
		{name: "descriptions", descriptions: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pr := pullRequest{Body: "Human introduction\n\nHuman footer", State: "open"}
			pr.Base.SHA, pr.Head.SHA = base, head
			if tc.closed {
				pr.State = "closed"
			}
			if tc.invalid {
				pr.Body += sectionStart
			}
			if tc.oversized {
				pr.Body = strings.Repeat("x", 65536)
			}
			oldSDL := `type Query { "old description" value: String }`
			newSDL := `type Query { "new description" value: String }`
			if tc.noOp {
				pr.Body = renderSection("", merge, head, false)
			}
			reads, writes := 0, 0
			var written string
			g := githubClient{token: "test-token", client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Fatal("missing authentication")
				}
				var response any
				switch {
				case r.Method == "GET" && r.URL.Path == "/repos/dagger/dagger/pulls/42":
					reads++
					response = pr
					if tc.changed && reads > 1 {
						changed := pr
						changed.Body += " concurrent edit"
						response = changed
					}
				case r.Method == "GET" && r.URL.Path == "/repos/dagger/dagger/compare/"+base+"..."+head:
					response = map[string]any{"merge_base_commit": map[string]string{"sha": merge}}
				case r.Method == "PATCH" && r.URL.Path == "/repos/dagger/dagger/pulls/42":
					writes++
					var payload map[string]string
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if len(payload) != 1 {
						t.Fatal("must only patch body")
					}
					written = payload["body"]
					response = map[string]string{}
				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
					return nil, fmt.Errorf("unexpected request")
				}
				data, _ := json.Marshal(response)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			})}}
			result, err := g.updatePR(context.Background(), "dagger/dagger", "42", tc.descriptions, tc.publish,
				func(_ context.Context, repo, b, h string) (string, string, error) {
					if repo != "dagger/dagger" || b != merge || h != head {
						t.Fatal("must compare immutable head against merge base, not latest base")
					}
					return oldSDL, newSDL, nil
				})
			fails := tc.changed || tc.closed || tc.invalid || tc.oversized
			if (err != nil) != fails {
				t.Fatalf("result=%q err=%v", result, err)
			}
			wantWrites := 0
			if tc.publish && !fails && !tc.noOp {
				wantWrites = 1
			}
			if writes != wantWrites {
				t.Fatalf("got %d writes, want %d", writes, wantWrites)
			}
			if writes == 1 && (!strings.HasPrefix(written, pr.Body+"\n\n") || !strings.Contains(written, "No semantic API changes.")) {
				t.Fatal(written)
			}
			if tc.descriptions && !strings.Contains(result, "new description") {
				t.Fatal(result)
			}
		})
	}
}

func TestRunPRMissingToken(t *testing.T) {
	t.Setenv("GH_TOKEN", " \n\t")
	if err := runPR(context.Background(), "https://github.com/dagger/dagger/pull/42", false, false); err == nil || err.Error() != "GH_TOKEN is required" {
		t.Fatalf("expected missing token error, got %v", err)
	}
}

func TestGitHubFailureDoesNotPublish(t *testing.T) {
	g := githubClient{token: "secret", client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Fatal("must not write after API failure")
		}
		return &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden", Body: io.NopCloser(strings.NewReader("sensitive response"))}, nil
	})}}
	_, err := g.updatePR(context.Background(), "dagger/dagger", "42", false, true, nil)
	if err == nil || !strings.Contains(err.Error(), "403 Forbidden") || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPRURL(t *testing.T) {
	for _, url := range []string{"https://github.com/dagger/dagger/pull/42", "https://github.com/a/b/pull/1/"} {
		if !prURLPattern.MatchString(url) {
			t.Fatal(url)
		}
	}
	for _, url := range []string{"42", "http://github.com/a/b/pull/1", "https://evil.example/a/b/pull/1", "https://github.com/a/b/pull/1?token=x", "https://github.com/a/b/pull/0"} {
		if prURLPattern.MatchString(url) {
			t.Fatal(url)
		}
	}
}
