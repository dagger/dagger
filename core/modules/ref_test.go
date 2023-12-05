package modules_test

import (
	"net/url"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/daggertest"
	"dagger.io/dagger/querybuilder"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core/modules"
)

var github = &url.URL{
	Scheme: "https",
	Host:   "github.com",
	Path:   "/",
}

var exampleOrg = &url.URL{
	Scheme: "https",
	Host:   "example.org",
	Path:   "/",
}

type refParseExample struct {
	ref string
	res *modules.Ref
	err string
}

func (example refParseExample) Test(t *testing.T) {
	t.Helper()

	t.Run(example.ref, func(t *testing.T) {
		ref, err := modules.ParseRef(example.ref)
		if example.err != "" {
			require.ErrorContains(t, err, example.err)
		} else {
			require.NoError(t, err)
			require.Equal(t, example.res, ref)
		}
	})
}

func TestParseLocalRef(t *testing.T) {
	for _, example := range []refParseExample{
		{
			ref: ".",
			res: &modules.Ref{
				Source: modules.Source{
					Local: &modules.LocalSource{
						Path: ".",
					},
				},
			},
		},
		{
			ref: "./",
			res: &modules.Ref{
				Source: modules.Source{
					Local: &modules.LocalSource{
						Path: ".",
					},
				},
			},
		},
		{
			ref: "..",
			res: &modules.Ref{
				Source: modules.Source{
					Local: &modules.LocalSource{
						Path: "..",
					},
				},
			},
		},
		{
			ref: "../",
			res: &modules.Ref{
				Source: modules.Source{
					Local: &modules.LocalSource{
						Path: "..",
					},
				},
			},
		},
		{
			ref: "./foo",
			res: &modules.Ref{
				Source: modules.Source{
					Local: &modules.LocalSource{
						Path: "foo",
					},
				},
			},
		},
		{
			ref: "./foo/bar",
			res: &modules.Ref{
				Source: modules.Source{
					Local: &modules.LocalSource{
						Path: "foo/bar",
					},
				},
			},
		},
		{
			ref: "./foo/bar/",
			res: &modules.Ref{
				Source: modules.Source{
					Local: &modules.LocalSource{
						Path: "foo/bar",
					},
				},
			},
		},
		{
			ref: "./foo/bar//baz",
			res: &modules.Ref{
				Source: modules.Source{
					Local: &modules.LocalSource{
						Path: "foo/bar/baz",
					},
				},
			},
		},
		{
			ref: "./foo/bar//baz/",
			res: &modules.Ref{
				Source: modules.Source{
					Local: &modules.LocalSource{
						Path: "foo/bar/baz",
					},
				},
			},
		},
		{
			ref: "foo",
			res: &modules.Ref{
				Source: modules.Source{
					Local: &modules.LocalSource{
						Path: "foo",
					},
				},
			},
		},
		{
			ref: "foo/bar",
			res: &modules.Ref{
				Source: modules.Source{
					Local: &modules.LocalSource{
						Path: "foo/bar",
					},
				},
			},
		},
	} {
		example.Test(t)
	}
}

func TestParseGitSchemelessRef(t *testing.T) {
	for _, example := range []refParseExample{
		{
			ref: "github.com/user/repo",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
					},
				},
			},
		},
		{
			ref: "example.org/user/repo",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: exampleOrg.JoinPath("user", "repo"),
					},
				},
			},
		},
		{
			ref: "example.org/user/repo/subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: exampleOrg.JoinPath("user", "repo", "subdir"),
					},
				},
			},
		},
		{
			ref: "github.com/user/repo//subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Dir:      "subdir",
					},
				},
			},
		},
		{
			ref: "example.org/user/repo//subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: exampleOrg.JoinPath("user", "repo"),
						Dir:      "subdir",
					},
				},
			},
		},
		{
			ref: "github.com/user/repo@abcdef",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Commit:   "abcdef",
					},
				},
				Hash: "abcdef",
			},
		},
		{
			ref: "github.com/user/repo:branch@abcdef",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Ref:      "branch",
						Commit:   "abcdef",
					},
				},
				Tag:  "branch",
				Hash: "abcdef",
			},
		},
		{
			ref: "github.com/user/repo:v1.2.3@abcdef",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Ref:      "v1.2.3",
						Commit:   "abcdef",
					},
				},
				Tag:  "v1.2.3",
				Hash: "abcdef",
			},
		},
		{
			ref: "github.com/user/repo//subdir:v1.2.3@abcdef",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Ref:      "v1.2.3",
						Commit:   "abcdef",
						Dir:      "subdir",
					},
				},
				Tag:  "v1.2.3",
				Hash: "abcdef",
			},
		},
		{
			// backwards compatibility
			ref: "github.com/user/repo/subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Dir:      "subdir",
					},
				},
			},
		},
		{
			// user/repo/subdir assumption is only for github.com/...
			ref: "example.org/user/repo/subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: exampleOrg.JoinPath("user", "repo", "subdir"),
					},
				},
			},
		},
	} {
		example.Test(t)
	}
}

func TestParseGitSchemeRef(t *testing.T) {
	for _, example := range []refParseExample{
		{
			ref: "git://github.com/user/repo",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
					},
				},
			},
		},
		{
			ref: "git://example.org/user/repo",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: exampleOrg.JoinPath("user", "repo"),
					},
				},
			},
		},
		{
			ref: "git://example.org/user/repo/subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: exampleOrg.JoinPath("user", "repo", "subdir"),
					},
				},
			},
		},
		{
			ref: "git://github.com/user/repo//subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Dir:      "subdir",
					},
				},
			},
		},
		{
			ref: "git://example.org/user/repo//subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: exampleOrg.JoinPath("user", "repo"),
						Dir:      "subdir",
					},
				},
			},
		},
		{
			ref: "git://github.com/user/repo@abcdef",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Commit:   "abcdef",
					},
				},
				Hash: "abcdef",
			},
		},
		{
			ref: "git://github.com/user/repo:branch@abcdef",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Ref:      "branch",
						Commit:   "abcdef",
					},
				},
				Tag:  "branch",
				Hash: "abcdef",
			},
		},
		{
			ref: "git://github.com/user/repo:v1.2.3@abcdef",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Ref:      "v1.2.3",
						Commit:   "abcdef",
					},
				},
				Tag:  "v1.2.3",
				Hash: "abcdef",
			},
		},
		{
			ref: "git://github.com/user/repo//subdir:v1.2.3@abcdef",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Ref:      "v1.2.3",
						Commit:   "abcdef",
						Dir:      "subdir",
					},
				},
				Tag:  "v1.2.3",
				Hash: "abcdef",
			},
		},
		{
			// backwards compatibility
			ref: "git://github.com/user/repo/subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Dir:      "subdir",
					},
				},
			},
		},
		{
			// user/repo/subdir assumption is only for github.com/
			ref: "git://example.org/user/repo/subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: exampleOrg.JoinPath("user", "repo", "subdir"),
					},
				},
			},
		},
	} {
		example.Test(t)
	}
}

func TestParseGHSchemeRef(t *testing.T) {
	for _, example := range []refParseExample{
		{
			ref: "gh:user/repo",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Dir:      "subdir",
					},
				},
			},
		},
		{
			ref: "gh:user/repo/subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Dir:      "subdir",
					},
				},
			},
		},
		{
			ref: "gh://user//repo",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Dir:      "subdir",
					},
				},
			},
		},
		{
			ref: "gh://user/repo//subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "repo"),
						Dir:      "subdir",
					},
				},
			},
		},
	} {
		example.Test(t)
	}
}

func TestParseDaggerverseSchemeRef(t *testing.T) {
	for _, example := range []refParseExample{
		{
			ref: "dv:user/subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "daggerverse"),
						Dir:      "subdir",
					},
				},
			},
		},
		{
			ref: "dv:user/subdir:v1",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "daggerverse"),
						Dir:      "subdir",
					},
				},
				Tag: "v1",
			},
		},
		{
			ref: "dv:user/subdir:v1@deadbeef",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "daggerverse"),
						Dir:      "subdir",
					},
				},
				Tag:  "v1",
				Hash: "deadbeef",
			},
		},
		{
			ref: "dv://user//subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: github.JoinPath("user", "daggerverse"),
						Dir:      "subdir",
					},
				},
			},
		},
		{
			ref: "dv://example.org/user//subdir",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: exampleOrg.JoinPath("user", "daggerverse"),
						Dir:      "subdir",
					},
				},
			},
		},
		{
			// NB: arguably we could support this and just have it merge into the //
			// part. as long as it's unambiguous.
			ref: "dv://user/repo/extra//subdir",
			err: "extra path after repo",
		},
	} {
		example.Test(t)
	}
}

type pinExample struct {
	ref string
	res *modules.Ref
}

func TestPinGitRef(t *testing.T) {
	daggerRepo := "https://github.com/dagger/dagger"
	daggerRepoURL, err := url.Parse(daggerRepo)
	require.NoError(t, err)

	// This test requires various forms of published tags which are a real pain
	// in the butt to set up, so we stub it out.
	conn := daggertest.NewConn(t)

	dag, ctx := daggertest.Connect(t, dagger.WithConn(conn))

	//// stubs for github.com/dagger/dagger
	conn.Stub(
		querybuilder.Query().
			Select("git").Arg("url", daggerRepo).
			Select("tags").Arg("patterns", []string{"refs/tags/v*"}),
		[]string{
			"v0.0.1",
			"v0.1.0",
			"v0.1.1",
			"v0.2.0",
			"v0.2.1",
			"v0.3.0",
			"v1.0.0",
			"v1.1.0",
			"v2.0.0",
			"v2.1.1",
		},
	)
	conn.Stub(
		querybuilder.Query().
			Select("git").Arg("url", daggerRepo).
			Select("tag").Arg("name", "v0.1.0").
			Select("commit"),
		"deadbeef010",
	)
	conn.Stub(
		querybuilder.Query().
			Select("git").Arg("url", daggerRepo).
			Select("tag").Arg("name", "v0.1.1").
			Select("commit"),
		"deadbeef011",
	)
	conn.Stub(
		querybuilder.Query().
			Select("git").Arg("url", daggerRepo).
			Select("tag").Arg("name", "v0.3.0").
			Select("commit"),
		"deadbeef030",
	)
	conn.Stub(
		querybuilder.Query().
			Select("git").Arg("url", daggerRepo).
			Select("tag").Arg("name", "v1.1.0").
			Select("commit"),
		"deadbeef110",
	)
	conn.Stub(
		querybuilder.Query().
			Select("git").Arg("url", daggerRepo).
			Select("tag").Arg("name", "v2.1.1").
			Select("commit"),
		// TODO: this actually returns 94120a6b0b70bfebad67a256beee8352dd7a67d7 due
		// to a bug in Buildkit, hence all the stubbing
		"deadbeef211",
	)

	//// stubs for github.com/dagger/dagger/sdk/go
	conn.Stub(
		querybuilder.Query().
			Select("git").Arg("url", daggerRepo).
			Select("tags").Arg("patterns", []string{"refs/tags/sdk/go/v*"}),
		[]string{
			"v0.0.1",
			"v0.1.0",
			"v0.1.1",
			"v0.2.0",
			"v0.2.1",
			"v0.3.0",
			"v1.0.0",
			"v1.1.0",
			"v2.0.0",
			"v2.1.1",
		},
	)
	conn.Stub(
		querybuilder.Query().
			Select("git").Arg("url", daggerRepo).
			Select("tag").Arg("name", "sdk/go/v2.1.1").
			Select("commit"),
		"sdkgodeadbeef211",
	)

	for _, example := range []pinExample{
		{
			ref: "github.com/dagger/dagger",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: daggerRepoURL,
						Ref:      "v2.1.1",
						Commit:   "deadbeef211",
					},
				},
				Tag:  "v2.1.1",
				Hash: "deadbeef211",
			},
		},
		{
			ref: "github.com/dagger/dagger:v0.1.0",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: daggerRepoURL,
						Ref:      "v0.1.0",
						Commit:   "deadbeef010",
					},
				},
				Tag:  "v0.1.0",
				Hash: "deadbeef010",
			},
		},
		{
			ref: "github.com/dagger/dagger:v0.1",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: daggerRepoURL,
						Ref:      "v0.1.1",
						Commit:   "deadbeef011",
					},
				},
				Tag:  "v0.1.1",
				Hash: "deadbeef011",
			},
		},
		{
			ref: "github.com/dagger/dagger:v0",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: daggerRepoURL,
						Ref:      "v0.3.0",
						Commit:   "deadbeef030",
					},
				},
				Tag:  "v0.3.0",
				Hash: "deadbeef030",
			},
		},
		{
			ref: "github.com/dagger/dagger:v1",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: daggerRepoURL,
						Ref:      "v1.1.0",
						Commit:   "deadbeef110",
					},
				},
				Tag:  "v1.1.0",
				Hash: "deadbeef110",
			},
		},
		{
			ref: "github.com/dagger/dagger@deadbeefpinned",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: daggerRepoURL,
						Commit:   "deadbeefpinned",
					},
				},
				Hash: "deadbeefpinned",
			},
		},
		{
			ref: "github.com/dagger/dagger//sdk/go:v2.1.1",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: daggerRepoURL,
						Ref:      "sdk/go/v2.1.1",
						Commit:   "sdkgodeadbeef211",
						Dir:      "sdk/go",
					},
				},
				Tag:  "v2.1.1",
				Hash: "sdkgodeadbeef211",
			},
		},
	} {
		t.Run(example.ref, func(t *testing.T) {
			ref, err := modules.ParseRef(example.ref)
			require.NoError(t, err)
			require.NoError(t, ref.Pin(ctx, dag))
			require.Equal(t, example.res, ref)
		})
	}
}
