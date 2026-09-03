// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vcs

import (
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"testing"
)

// Test that RepoRootForImportPath creates the correct RepoRoot for a given importPath.
// TODO(cmang): Add tests for SVN and BZR.
func TestRepoRootForImportPath(t *testing.T) {
	if runtime.GOOS == "android" {
		t.Skipf("incomplete source tree on %s", runtime.GOOS)
	}

	tests := []struct {
		path string
		want *RepoRoot
	}{
		{
			"github.com/golang/groupcache/foo",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://github.com/golang/groupcache",
				Root: "github.com/golang/groupcache",
			},
		},
		{
			"github.com/golang/groupcache.git/foo",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://github.com/golang/groupcache",
				Root: "github.com/golang/groupcache.git",
			},
		},
		{
			"github.com/dagger/dagger-test-modules/../..",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://github.com/dagger/dagger-test-modules",
				Root: "github.com/dagger/dagger-test-modules",
			},
		},
		{
			"github.com/dagger/dagger-test-modules/../../",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://github.com/dagger/dagger-test-modules",
				Root: "github.com/dagger/dagger-test-modules",
			},
		},
		// Unicode letters are allowed in import paths.
		// issue https://github.com/golang/go/issues/18660
		{
			"github.com/user/unicode/испытание",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://github.com/user/unicode",
				Root: "github.com/user/unicode",
			},
		},
		// IBM DevOps Services tests
		{
			"hub.jazz.net/git/user1/pkgname",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://hub.jazz.net/git/user1/pkgname",
				Root: "hub.jazz.net/git/user1/pkgname",
			},
		},
		{
			"hub.jazz.net/git/user1/pkgname/submodule/submodule/submodule",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://hub.jazz.net/git/user1/pkgname",
				Root: "hub.jazz.net/git/user1/pkgname",
			},
		},
		// Trailing .git is less preferred but included for
		// compatibility purposes while the same source needs to
		// be compilable on both old and new go
		{
			"git.openstack.org/openstack/swift.git",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://git.openstack.org/openstack/swift.git",
				Root: "git.openstack.org/openstack/swift.git",
			},
		},
		{
			"git.openstack.org/openstack/swift/go/hummingbird",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://git.openstack.org/openstack/swift",
				Root: "git.openstack.org/openstack/swift",
			},
		},
		{
			"git.apache.org/package-name.git",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://git.apache.org/package-name.git",
				Root: "git.apache.org/package-name.git",
			},
		},
		{
			"git.apache.org/package-name_2.x.git/path/to/lib",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://git.apache.org/package-name_2.x.git",
				Root: "git.apache.org/package-name_2.x.git",
			},
		},
		// HACK: temporarily disabled because of go-away (https://drewdevault.com/2025/03/17/2025-03-17-Stop-externalizing-your-costs-on-me.html)
		// this seems to accidentally catch go-get as well, e.g. `GOPRIVATE=git.sr.ht go get git.sr.ht/~jacqueline/tangara-fw/lib` fails with `403 Forbidden`
		// {
		// 	"git.sr.ht/~jacqueline/tangara-fw/lib",
		// 	&RepoRoot{
		// 		VCS:  vcsGit,
		// 		Repo: "https://git.sr.ht/~jacqueline/tangara-fw",
		// 		Root: "git.sr.ht/~jacqueline/tangara-fw",
		// 	},
		// },
		// { FAILS as returns 404 without tags
		// 	"git.sr.ht/~jacqueline/tangara-fw.git/lib",
		// 	&RepoRoot{
		// 		VCS:  vcsGit,
		// 		Repo: "https://git.sr.ht/~jacqueline/tangara-fw.git",
		// 	},
		// },
		{
			"bitbucket.org/workspace/pkgname",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://bitbucket.org/workspace/pkgname",
				Root: "bitbucket.org/workspace/pkgname",
			},
		},
		{
			"bitbucket.org/workspace/pkgname/../..",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://bitbucket.org/workspace/pkgname",
				Root: "bitbucket.org/workspace/pkgname",
			},
		},
		{
			"bitbucket.org/workspace/pkgname/../../",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://bitbucket.org/workspace/pkgname",
				Root: "bitbucket.org/workspace/pkgname",
			},
		},
		{
			"bitbucket.org/workspace/pkgname.git",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://bitbucket.org/workspace/pkgname",
				Root: "bitbucket.org/workspace/pkgname.git",
			},
		},
		// GitLab public repo with an explicit repository boundary.
		{
			"gitlab.com/testguigui1/dagger-public-sub/mywork.git/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork",
				Root: "gitlab.com/testguigui1/dagger-public-sub/mywork.git",
			},
		},
		{
			"bitbucket.org/workspace/pkgname/subdir",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://bitbucket.org/workspace/pkgname",
				Root: "bitbucket.org/workspace/pkgname",
			},
		},
		{
			"codeberg.org/workspace/pkgname/subdir",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://codeberg.org/workspace/pkgname",
				Root: "codeberg.org/workspace/pkgname",
			},
		},

		// Azure DevOps
		// Cloud (Azure DevOps Services): short HTTPS ref, with format: user, org and where repo name == org name
		{
			"dev.azure.com/dagger-e2e/_git/dagger-modules-test-public/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://dev.azure.com/dagger-e2e/_git/dagger-modules-test-public",
				Root: "dev.azure.com/dagger-e2e/_git/dagger-modules-test-public",
			},
		},
		{
			"dev.azure.com/dagger-e2e/_git/dagger-modules-test-public.git/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://dev.azure.com/dagger-e2e/_git/dagger-modules-test-public",
				Root: "dev.azure.com/dagger-e2e/_git/dagger-modules-test-public.git",
			},
		},
		// Cloud (Azure DevOps Services): HTTPS ref, with format: user, org and repo name != org name
		{
			"dev.azure.com/daggere2e/public/_git/dagger-test-modules/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://dev.azure.com/daggere2e/public/_git/dagger-test-modules",
				Root: "dev.azure.com/daggere2e/public/_git/dagger-test-modules",
			},
		},
		// ⚠️ Azure requires auth when cloning on this format, will have to be used conjointly with PAT
		{
			"dev.azure.com/daggere2e/public/_git/dagger-test-modules.git/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://dev.azure.com/daggere2e/public/_git/dagger-test-modules",
				Root: "dev.azure.com/daggere2e/public/_git/dagger-test-modules.git",
			},
		},
		{
			"dev.azure.com/dagger%20e2e/public/_git/dagger%20test%20modules.git/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://dev.azure.com/dagger%20e2e/public/_git/dagger%20test%20modules",
				Root: "dev.azure.com/dagger%20e2e/public/_git/dagger%20test%20modules.git",
			},
		},
		{
			"dev.azure.com/dagger e2e/public/_git/dagger test modules.git/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://dev.azure.com/dagger e2e/public/_git/dagger test modules",
				Root: "dev.azure.com/dagger e2e/public/_git/dagger test modules.git",
			},
		},
		// Cloud (Azure DevOps Services): SSH ref - new ref style
		// https://learn.microsoft.com/en-us/azure/devops/repos/git/use-ssh-keys-to-authenticate?view=azure-devops
		{
			"ssh.dev.azure.com/v3/daggere2e/private/dagger-test-modules/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://dev.azure.com/daggere2e/private/_git/dagger-test-modules",
				Root: "ssh.dev.azure.com/v3/daggere2e/private/dagger-test-modules",
			},
		},
		{
			"ssh.dev.azure.com/v3/daggere2e/private/dagger-test-modules.git/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://dev.azure.com/daggere2e/private/_git/dagger-test-modules",
				Root: "ssh.dev.azure.com/v3/daggere2e/private/dagger-test-modules.git",
			},
		},
		{
			"ssh.dev.azure.com/v3/dagger%20e2e/private/dagger%20test%20modules.git/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://dev.azure.com/dagger%20e2e/private/_git/dagger%20test%20modules",
				Root: "ssh.dev.azure.com/v3/dagger%20e2e/private/dagger%20test%20modules.git",
			},
		},
		{
			"ssh.dev.azure.com/v3/dagger e2e/private/dagger test modules.git/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://dev.azure.com/dagger e2e/private/_git/dagger test modules",
				Root: "ssh.dev.azure.com/v3/dagger e2e/private/dagger test modules.git",
			},
		},
		// On-prem (Azure DevOps Server)
		{
			"azure.example.com/tfs/collection/project/_git/repository/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://azure.example.com/tfs/collection/project/_git/repository",
				Root: "azure.example.com/tfs/collection/project/_git/repository",
			},
		},

		// General syntax for any server
		{
			"extranet.example.com/bitbucket/scm/~user/daggerverse.git/pvc_migrate",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://extranet.example.com/bitbucket/scm/~user/daggerverse",
				Root: "extranet.example.com/bitbucket/scm/~user/daggerverse.git",
			},
		},
	}

	for _, test := range tests {
		got, err := RepoRootForImportPath(test.path, true)
		if err != nil {
			t.Errorf("RepoRootForImportPath(%q): %v", test.path, err)
			continue
		}
		want := test.want
		if want == nil {
			if got != nil {
				t.Errorf("RepoRootForImportPath(%q) = %v, want nil", test.path, got)
			}
			continue
		}
		if got.VCS == nil || want.VCS == nil {
			t.Errorf("RepoRootForImportPath(%q): got.VCS or want.VCS is nil", test.path)
			continue
		}
		if got.VCS.Name != want.VCS.Name || got.Repo != want.Repo {
			t.Errorf("RepoRootForImportPath(%q) = VCS(%s) Repo(%s), want VCS(%s) Repo(%s)", test.path, got.VCS, got.Repo, want.VCS, want.Repo)
		}
		if got.Root != want.Root {
			t.Errorf("RepoRootForImportPath(%q) = VCS(%s) Root(%s), want VCS(%s) Root(%s)", test.path, got.VCS, got.Root, want.VCS, want.Root)
		}
	}
}

// Test that FromDir correctly inspects a given directory and returns the right VCS and root.
func TestFromDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vcstest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	for j, vcs := range vcsList {
		dir := filepath.Join(tempDir, "example.com", vcs.Name, "."+vcs.Cmd)
		if j&1 == 0 {
			err := os.MkdirAll(dir, 0755)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			err := os.MkdirAll(filepath.Dir(dir), 0755)
			if err != nil {
				t.Fatal(err)
			}
			f, err := os.Create(dir)
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
		}

		want := RepoRoot{
			VCS:  vcs,
			Root: path.Join("example.com", vcs.Name),
		}
		var got RepoRoot
		got.VCS, got.Root, err = FromDir(dir, tempDir)
		if err != nil {
			t.Errorf("FromDir(%q, %q): %v", dir, tempDir, err)
			continue
		}
		if got.VCS.Name != want.VCS.Name || got.Root != want.Root {
			t.Errorf("FromDir(%q, %q) = VCS(%s) Root(%s), want VCS(%s) Root(%s)", dir, tempDir, got.VCS, got.Root, want.VCS, want.Root)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestRepoRootForImportPathDoesNotRequestGoVanityMetadata(t *testing.T) {
	originalHTTPClient := httpClient
	t.Cleanup(func() { httpClient = originalHTTPClient })

	requested := false
	httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requested = true
		return nil, errors.New("unexpected HTTP request")
	})}

	_, err := RepoRootForImportPath("vanity.example/foo", true)
	if err == nil {
		t.Fatal("expected an unrecognized import path error")
	}
	if requested {
		t.Fatal("unexpected Go vanity metadata request")
	}
}
