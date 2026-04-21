package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestOriginToPath(t *testing.T) {
	for _, tc := range []struct {
		origin string
		want   string
	}{
		{
			origin: "ssh://git@github.com/shykes/daggerverse",
			want:   "github.com/shykes/daggerverse",
		},
		{
			origin: "ssh://git@github.com/shykes/daggerverse.git",
			want:   "github.com/shykes/daggerverse",
		},
		{
			origin: "git@github.com:sipsma/daggerverse",
			want:   "github.com/sipsma/daggerverse",
		},
		{
			origin: "git@github.com:sipsma/daggerverse.git",
			want:   "github.com/sipsma/daggerverse",
		},
		{
			origin: "https://github.com/sipsma/daggerverse",
			want:   "github.com/sipsma/daggerverse",
		},
		{
			origin: "https://github.com/sipsma/daggerverse.git",
			want:   "github.com/sipsma/daggerverse",
		},
	} {
		p, err := originToPath(tc.origin)
		require.NoError(t, err)
		require.Equal(t, tc.want, p)
	}
}

// This covers cases that the full integ test in core/integration/module_test.go can't
// cover right now due to limitation in needing real SSH keys to test e2e.
func TestParseGit(t *testing.T) {
	for _, tc := range []struct {
		urlStr     string
		want       *gitutil.GitURL
		wantRemote string
	}{
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse",
			want: &gitutil.GitURL{
				Scheme:   "ssh",
				User:     url.User("git"),
				Host:     "github.com",
				Path:     "/shykes/daggerverse",
				Fragment: nil,
			},
			wantRemote: "ssh://git@github.com/shykes/daggerverse",
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse.git",
			want: &gitutil.GitURL{
				Scheme:   "ssh",
				User:     url.User("git"),
				Host:     "github.com",
				Path:     "/shykes/daggerverse.git",
				Fragment: nil,
			},
			wantRemote: "ssh://git@github.com/shykes/daggerverse.git",
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse#v0.9.1",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "/shykes/daggerverse",
				Fragment: &gitutil.GitURLFragment{
					Ref: "v0.9.1",
				},
			},
			wantRemote: "ssh://git@github.com/shykes/daggerverse",
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse.git#v0.9.1",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "/shykes/daggerverse.git",
				Fragment: &gitutil.GitURLFragment{
					Ref: "v0.9.1",
				},
			},
			wantRemote: "ssh://git@github.com/shykes/daggerverse.git",
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse#v0.9.1:subdir1/subdir2",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "/shykes/daggerverse",
				Fragment: &gitutil.GitURLFragment{
					Ref:    "v0.9.1",
					Subdir: "subdir1/subdir2",
				},
			},
			wantRemote: "ssh://git@github.com/shykes/daggerverse",
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse.git#v0.9.1:subdir1/subdir2",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "/shykes/daggerverse.git",
				Fragment: &gitutil.GitURLFragment{
					Ref:    "v0.9.1",
					Subdir: "subdir1/subdir2",
				},
			},
			wantRemote: "ssh://git@github.com/shykes/daggerverse.git",
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse",
			want: &gitutil.GitURL{
				Scheme:   "ssh",
				User:     url.User("git"),
				Host:     "github.com",
				Path:     "sipsma/daggerverse",
				Fragment: nil,
			},
			wantRemote: "git@github.com:sipsma/daggerverse",
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse.git",
			want: &gitutil.GitURL{
				Scheme:   "ssh",
				User:     url.User("git"),
				Host:     "github.com",
				Path:     "sipsma/daggerverse.git",
				Fragment: nil,
			},
			wantRemote: "git@github.com:sipsma/daggerverse.git",
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse#v0.9.1",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "sipsma/daggerverse",
				Fragment: &gitutil.GitURLFragment{
					Ref: "v0.9.1",
				},
			},
			wantRemote: "git@github.com:sipsma/daggerverse",
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse.git#v0.9.1",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "sipsma/daggerverse.git",
				Fragment: &gitutil.GitURLFragment{
					Ref: "v0.9.1",
				},
			},
			wantRemote: "git@github.com:sipsma/daggerverse.git",
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse#v0.9.1:subdir1/subdir2",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "sipsma/daggerverse",
				Fragment: &gitutil.GitURLFragment{
					Ref:    "v0.9.1",
					Subdir: "subdir1/subdir2",
				},
			},
			wantRemote: "git@github.com:sipsma/daggerverse",
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse.git#v0.9.1:subdir1/subdir2",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "sipsma/daggerverse.git",
				Fragment: &gitutil.GitURLFragment{
					Ref:    "v0.9.1",
					Subdir: "subdir1/subdir2",
				},
			},
			wantRemote: "git@github.com:sipsma/daggerverse.git",
		},
	} {
		t.Run(tc.urlStr, func(t *testing.T) {
			t.Parallel()
			parsedGit, err := gitutil.ParseURL(tc.urlStr)
			require.NoError(t, err)
			require.NotNil(t, parsedGit)
			require.Equal(t, tc.want.Scheme, parsedGit.Scheme)
			require.Equal(t, tc.want.Host, parsedGit.Host)
			require.Equal(t, tc.want.Path, parsedGit.Path)
			require.Equal(t, tc.want.Fragment, parsedGit.Fragment)
			require.Equal(t, tc.want.User.String(), parsedGit.User.String())
			require.Equal(t, tc.wantRemote, parsedGit.Remote())
		})
	}
}

func TestSetGoSDKSkipRuntimeCodegen(t *testing.T) {
	cases := []struct {
		name           string
		input          string
		writeGitignore bool
	}{
		{
			name:  "no codegen key",
			input: `{"name":"m","engineVersion":"latest","sdk":{"source":"go"}}`,
		},
		{
			name:  "existing codegen with automaticGitignore true",
			input: `{"name":"m","engineVersion":"latest","sdk":{"source":"go"},"codegen":{"automaticGitignore":true}}`,
		},
		{
			name: "preserves toolchains and clients",
			input: `{
				"name": "m",
				"engineVersion": "latest",
				"sdk": {"source": "go"},
				"toolchains": [{"source": "./t"}],
				"clients": [{"generator": "go", "directory": "."}]
			}`,
		},
		{
			name:           "removes sibling .gitignore",
			input:          `{"name":"m","engineVersion":"latest","sdk":{"source":"go"}}`,
			writeGitignore: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "dagger.json")
			if err := os.WriteFile(path, []byte(tc.input), 0o644); err != nil {
				t.Fatalf("write input: %v", err)
			}

			gitignorePath := filepath.Join(dir, ".gitignore")
			if tc.writeGitignore {
				if err := os.WriteFile(gitignorePath, []byte("/dagger.gen.go\n/internal/dagger\n"), 0o644); err != nil {
					t.Fatalf("write .gitignore: %v", err)
				}
			}

			if err := setGoSDKSkipRuntimeCodegen(path); err != nil {
				t.Fatalf("setGoSDKSkipRuntimeCodegen: %v", err)
			}

			out, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read output: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("parse output: %v", err)
			}

			codegen, ok := got["codegen"].(map[string]any)
			if !ok {
				t.Fatalf("codegen key missing or wrong type: %v", got["codegen"])
			}
			if v, ok := codegen["automaticGitignore"].(bool); !ok || v {
				t.Errorf("automaticGitignore: got %v, want false", codegen["automaticGitignore"])
			}
			if v, ok := codegen["legacyCodegenAtRuntime"].(bool); !ok || v {
				t.Errorf("legacyCodegenAtRuntime: got %v, want false", codegen["legacyCodegenAtRuntime"])
			}

			// Unknown-key preservation check applies to the third case only.
			if tc.name == "preserves toolchains and clients" {
				if _, ok := got["toolchains"]; !ok {
					t.Errorf("toolchains key not preserved; got: %s", out)
				}
				if _, ok := got["clients"]; !ok {
					t.Errorf("clients key not preserved; got: %s", out)
				}
			}

			// Output must end with a trailing newline.
			if !strings.HasSuffix(string(out), "\n") {
				t.Errorf("output missing trailing newline")
			}

			// Sibling .gitignore (when present) must be removed, so that
			// Host.directory(gitignore:true) does not continue to exclude
			// the committed generated files.
			if tc.writeGitignore {
				if _, err := os.Stat(gitignorePath); !os.IsNotExist(err) {
					t.Errorf(".gitignore was not removed: stat err=%v", err)
				}
			}
		})
	}
}

func TestSetGoSDKSkipRuntimeCodegen_NoGitignoreToRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dagger.json")
	input := `{"name":"m","engineVersion":"latest","sdk":{"source":"go"}}`
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	// No sibling .gitignore exists — the helper must silently succeed.
	if err := setGoSDKSkipRuntimeCodegen(path); err != nil {
		t.Fatalf("setGoSDKSkipRuntimeCodegen: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("unexpected .gitignore after call: stat err=%v", err)
	}
}

func TestSetGoSDKSkipRuntimeCodegen_MissingFile(t *testing.T) {
	err := setGoSDKSkipRuntimeCodegen(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "read") {
		t.Errorf("error should mention 'read', got: %v", err)
	}
}
