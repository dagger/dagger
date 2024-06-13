// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vcs

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
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
			},
		},
		{
			"github.com/golang/groupcache.git/foo",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://github.com/golang/groupcache.git",
			},
		},
		{
			"github.com/dagger/dagger-test-modules/../..",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://github.com/dagger/dagger-test-modules",
			},
		},
		{
			"github.com/dagger/dagger-test-modules/../../",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://github.com/dagger/dagger-test-modules",
			},
		},
		// Unicode letters are allowed in import paths.
		// issue https://github.com/golang/go/issues/18660
		{
			"github.com/user/unicode/испытание",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://github.com/user/unicode",
			},
		},
		// IBM DevOps Services tests
		{
			"hub.jazz.net/git/user1/pkgname",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://hub.jazz.net/git/user1/pkgname",
			},
		},
		{
			"hub.jazz.net/git/user1/pkgname/submodule/submodule/submodule",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://hub.jazz.net/git/user1/pkgname",
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
			},
		},
		{
			"git.openstack.org/openstack/swift/go/hummingbird",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://git.openstack.org/openstack/swift",
			},
		},
		{
			"git.apache.org/package-name.git",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://git.apache.org/package-name.git",
			},
		},
		{
			"git.apache.org/package-name_2.x.git/path/to/lib",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://git.apache.org/package-name_2.x.git",
			},
		},
		{
			"git.sr.ht/~jacqueline/tangara-fw/lib",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://git.sr.ht/~jacqueline/tangara-fw",
			},
		},
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
			},
		},
		{
			"bitbucket.org/workspace/pkgname/../..",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://bitbucket.org/workspace/pkgname",
			},
		},
		{
			"bitbucket.org/workspace/pkgname/../../",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://bitbucket.org/workspace/pkgname",
			},
		},
		{
			"bitbucket.org/workspace/pkgname.git",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://bitbucket.org/workspace/pkgname.git",
			},
		},
		// GitLab public repo
		{
			"gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork.git",
			},
		},
		{
			"gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork.git",
			},
		},
		// GitLab private repo
		// behavior of private GitLab repos is different from public ones
		// https://gitlab.com/gitlab-org/gitlab-foss/-/blob/master/lib/gitlab/middleware/go.rb#L114-126
		// it relies on gitcredentials to authenticate
		// todo(guillaume): rely on a dagger GitLab repo with a read-only PAT to test this
		// {
		// 	"gitlab.com/testguigui1/awesomesubgroup/mywork/depth1/depth2", // private subgroup
		// 	&RepoRoot{
		// 		VCS:  vcsGit,
		// 		Repo: "https://gitlab.com/testguigui1/awesomesubgroup.git", // false positive returned by GitLab for privacy purpose
		// 	},
		// },
		// {
		// 	"gitlab.com/testguigui1/awesomesubgroup/mywork.git/depth1/depth2", // private subgroup
		// 	&RepoRoot{
		// 		VCS:  vcsGit,
		// 		Repo: "https://gitlab.com/testguigui1/awesomesubgroup/mywork",
		// 	},
		// },
		{ // vanity URL, TODO: improve test by changing dagger's redirection
			"dagger.io/dagger",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://github.com/dagger/dagger-go-sdk",
			},
		},
		{
			"bitbucket.org/workspace/pkgname/subdir",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://bitbucket.org/workspace/pkgname",
			},
		},
		{
			"codeberg.org/workspace/pkgname/subdir",
			&RepoRoot{
				VCS:  vcsGit,
				Repo: "https://codeberg.org/workspace/pkgname",
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

var parseMetaGoImportsTests = []struct {
	in  string
	out []metaImport
}{
	{
		`<meta name="go-import" content="foo/bar git https://github.com/rsc/foo/bar">`,
		[]metaImport{{"foo/bar", "git", "https://github.com/rsc/foo/bar"}},
	},
	{
		`<meta name="go-import" content="foo/bar git https://github.com/rsc/foo/bar">
		<meta name="go-import" content="baz/quux git http://github.com/rsc/baz/quux">`,
		[]metaImport{
			{"foo/bar", "git", "https://github.com/rsc/foo/bar"},
			{"baz/quux", "git", "http://github.com/rsc/baz/quux"},
		},
	},
	{
		`<meta name="go-import" content="foo/bar git https://github.com/rsc/foo/bar">
		<meta name="go-import" content="foo/bar mod http://github.com/rsc/baz/quux">`,
		[]metaImport{
			{"foo/bar", "git", "https://github.com/rsc/foo/bar"},
		},
	},
	{
		`<meta name="go-import" content="foo/bar mod http://github.com/rsc/baz/quux">
		<meta name="go-import" content="foo/bar git https://github.com/rsc/foo/bar">`,
		[]metaImport{
			{"foo/bar", "git", "https://github.com/rsc/foo/bar"},
		},
	},
	{
		`<head>
		<meta name="go-import" content="foo/bar git https://github.com/rsc/foo/bar">
		</head>`,
		[]metaImport{{"foo/bar", "git", "https://github.com/rsc/foo/bar"}},
	},
	{
		`<meta name="go-import" content="foo/bar git https://github.com/rsc/foo/bar">
		<body>`,
		[]metaImport{{"foo/bar", "git", "https://github.com/rsc/foo/bar"}},
	},
	{
		`<!doctype html><meta name="go-import" content="foo/bar git https://github.com/rsc/foo/bar">`,
		[]metaImport{{"foo/bar", "git", "https://github.com/rsc/foo/bar"}},
	},
	{
		// XML doesn't like <div style=position:relative>.
		`<!doctype html><title>Page Not Found</title><meta name=go-import content="chitin.io/chitin git https://github.com/chitin-io/chitin"><div style=position:relative>DRAFT</div>`,
		[]metaImport{{"chitin.io/chitin", "git", "https://github.com/chitin-io/chitin"}},
	},
	{
		`<meta name="go-import" content="myitcv.io git https://github.com/myitcv/x">
	        <meta name="go-import" content="myitcv.io/blah2 mod https://raw.githubusercontent.com/myitcv/pubx/master">
	        `,
		[]metaImport{{"myitcv.io", "git", "https://github.com/myitcv/x"}},
	},
}

func TestParseMetaGoImports(t *testing.T) {
	for i, tt := range parseMetaGoImportsTests {
		out, err := parseMetaGoImports(strings.NewReader(tt.in))
		if err != nil {
			t.Errorf("test#%d: %v", i, err)
			continue
		}
		if !reflect.DeepEqual(out, tt.out) {
			t.Errorf("test#%d:\n\thave %q\n\twant %q", i, out, tt.out)
		}
	}
}

func TestValidateRepoRoot(t *testing.T) {
	tests := []struct {
		root string
		ok   bool
	}{
		{
			root: "",
			ok:   false,
		},
		{
			root: "http://",
			ok:   true,
		},
		{
			root: "git+ssh://",
			ok:   true,
		},
		{
			root: "http#://",
			ok:   false,
		},
		{
			root: "-config",
			ok:   false,
		},
		{
			root: "-config://",
			ok:   false,
		},
	}

	for _, test := range tests {
		err := validateRepoRoot(test.root)
		ok := err == nil
		if ok != test.ok {
			want := "error"
			if test.ok {
				want = "nil"
			}
			t.Errorf("validateRepoRoot(%q) = %q, want %s", test.root, err, want)
		}
	}
}

func TestMatchGoImport(t *testing.T) {
	tests := []struct {
		imports []metaImport
		path    string
		mi      metaImport
		err     error
	}{
		{
			imports: []metaImport{
				{Prefix: "example.com/user/foo", VCS: "git", RepoRoot: "https://example.com/repo/target"},
			},
			path: "example.com/user/foo",
			mi:   metaImport{Prefix: "example.com/user/foo", VCS: "git", RepoRoot: "https://example.com/repo/target"},
		},
		{
			imports: []metaImport{
				{Prefix: "example.com/user/foo", VCS: "git", RepoRoot: "https://example.com/repo/target"},
			},
			path: "example.com/user/foo/",
			mi:   metaImport{Prefix: "example.com/user/foo", VCS: "git", RepoRoot: "https://example.com/repo/target"},
		},
		{
			imports: []metaImport{
				{Prefix: "example.com/user/foo", VCS: "git", RepoRoot: "https://example.com/repo/target"},
				{Prefix: "example.com/user/fooa", VCS: "git", RepoRoot: "https://example.com/repo/target"},
			},
			path: "example.com/user/foo",
			mi:   metaImport{Prefix: "example.com/user/foo", VCS: "git", RepoRoot: "https://example.com/repo/target"},
		},
		{
			imports: []metaImport{
				{Prefix: "example.com/user/foo", VCS: "git", RepoRoot: "https://example.com/repo/target"},
				{Prefix: "example.com/user/fooa", VCS: "git", RepoRoot: "https://example.com/repo/target"},
			},
			path: "example.com/user/fooa",
			mi:   metaImport{Prefix: "example.com/user/fooa", VCS: "git", RepoRoot: "https://example.com/repo/target"},
		},
		{
			imports: []metaImport{
				{Prefix: "example.com/user/foo", VCS: "git", RepoRoot: "https://example.com/repo/target"},
				{Prefix: "example.com/user/foo/bar", VCS: "git", RepoRoot: "https://example.com/repo/target"},
			},
			path: "example.com/user/foo/bar",
			err:  errors.New("should not be allowed to create nested repo"),
		},
		{
			imports: []metaImport{
				{Prefix: "example.com/user/foo", VCS: "git", RepoRoot: "https://example.com/repo/target"},
				{Prefix: "example.com/user/foo/bar", VCS: "git", RepoRoot: "https://example.com/repo/target"},
			},
			path: "example.com/user/foo/bar/baz",
			err:  errors.New("should not be allowed to create nested repo"),
		},
		{
			imports: []metaImport{
				{Prefix: "example.com/user/foo", VCS: "git", RepoRoot: "https://example.com/repo/target"},
				{Prefix: "example.com/user/foo/bar", VCS: "git", RepoRoot: "https://example.com/repo/target"},
			},
			path: "example.com/user/foo/bar/baz/qux",
			err:  errors.New("should not be allowed to create nested repo"),
		},
		{
			imports: []metaImport{
				{Prefix: "example.com/user/foo", VCS: "git", RepoRoot: "https://example.com/repo/target"},
				{Prefix: "example.com/user/foo/bar", VCS: "git", RepoRoot: "https://example.com/repo/target"},
			},
			path: "example.com/user/foo/bar/baz/",
			err:  errors.New("should not be allowed to create nested repo"),
		},
		{
			imports: []metaImport{
				{Prefix: "example.com/user/foo", VCS: "git", RepoRoot: "https://example.com/repo/target"},
				{Prefix: "example.com/user/foo/bar", VCS: "git", RepoRoot: "https://example.com/repo/target"},
			},
			path: "example.com",
			err:  errors.New("pathologically short path"),
		},
	}

	for _, test := range tests {
		mi, err := matchGoImport(test.imports, test.path)
		if mi != test.mi {
			t.Errorf("unexpected metaImport; got %v, want %v", mi, test.mi)
		}

		got := err
		want := test.err
		if (got == nil) != (want == nil) {
			t.Errorf("unexpected error; got %v, want %v", got, want)
		}
	}
}
