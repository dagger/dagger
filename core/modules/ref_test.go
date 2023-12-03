package modules_test

import (
	"net/url"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/daggertest"
)

type refParseExample struct {
	ref string
	res *modules.Ref
	err string
}

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
			// user/repo/subdir assumption is only for github.com/...
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
			ref: "gh://user//subdir",
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
		{
			// NB: arguably we could support this and just have it merge into the //
			// part. as long as it's unambiguous.
			ref: "gh://user/repo/extra//subdir",
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

	conn := new(daggertest.Conn)

	//// stubs for github.com/dagger/dagger
	conn.Stub(
		// query{git(url:"https://github.com/dagger/dagger"){tags(patterns:["refs/tags/v*"])}}
		querybuilder.Query().
			Select("git").Arg("url", daggerRepo).
			Select("tags").Arg("patterns", []string{"refs/tags/v*"}),
		[]string{
			"v0.1.0",
			"v0.9.3",
		},
	)
	conn.Stub(
		// query{git(url:"https://github.com/dagger/dagger"){tag(name:"v0.1.0"){commit}}}
		querybuilder.Query().
			Select("git").Arg("url", daggerRepo).
			Select("tag").Arg("name", "v0.1.0").
			Select("commit"),
		"eebd91820b701259841f818aaded760c3106bcc3",
	)
	conn.Stub(
		// query{git(url:"https://github.com/dagger/dagger"){tag(name:"v0.9.3"){commit}}}
		querybuilder.Query().
			Select("git").Arg("url", daggerRepo).
			Select("tag").Arg("name", "v0.9.3").
			Select("commit"),
		// TODO: this actually returns 94120a6b0b70bfebad67a256beee8352dd7a67d7 due
		// to a bug in Buildkit, hence all the stubbing
		"d44c734dbbbcecc75507003c07acabb16375891d",
	)

	//// stubs for github.com/dagger/dagger/sdk/go
	conn.Stub(
		// query{git(url:"https://github.com/dagger/dagger"){tags(patterns:["refs/tags/sdk/go/v*"])}}
		querybuilder.Query().
			Select("git").Arg("url", daggerRepo).
			Select("tags").Arg("patterns", []string{"refs/tags/sdk/go/v*"}),
		[]string{"v0.1.0", "v0.9.3"},
	)
	conn.Stub(
		// query{git(url:"https://github.com/dagger/dagger"){tag(name:"sdk/go/v0.9.3"){commit}}}
		querybuilder.Query().
			Select("git").Arg("url", daggerRepo).
			Select("tag").Arg("name", "sdk/go/v0.9.3").
			Select("commit"),
		"94120a6b0b70bfebad67a256beee8352dd7a67d7",
	)

	dag, ctx := daggertest.Connect(t, dagger.WithConn(conn))

	daggerRepoURL, err := url.Parse(daggerRepo)
	require.NoError(t, err)

	// NB: this _should_ not cause a race condition since the Pin call should
	// just do a cache hit
	var latestVersion *semver.Version
	daggerTags, err := dag.Git(daggerRepo).Tags(ctx, dagger.GitRepositoryTagsOpts{
		Patterns: []string{"refs/tags/v*"},
	})
	require.NoError(t, err)
	for _, t := range daggerTags {
		v, err := semver.NewVersion(t)
		if err == nil {
			if latestVersion == nil || v.GreaterThan(latestVersion) {
				latestVersion = v
			}
		}
	}
	latestTag := latestVersion.Original()
	latestCommit, err := dag.Git(daggerRepo).Tag(latestTag).Commit(ctx)
	require.NoError(t, err)

	for _, example := range []pinExample{
		{
			ref: "github.com/dagger/dagger",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: daggerRepoURL,
						Ref:      latestTag,
						Commit:   latestCommit,
					},
				},
				Tag:  latestTag,
				Hash: latestCommit,
			},
		},
		{
			ref: "github.com/dagger/dagger:v0.1.0",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: daggerRepoURL,
						Ref:      "v0.1.0",
						Commit:   "eebd91820b701259841f818aaded760c3106bcc3",
					},
				},
				Tag:  "v0.1.0",
				Hash: "eebd91820b701259841f818aaded760c3106bcc3",
			},
		},
		{
			ref: "github.com/dagger/dagger@d44c734dbbbcecc75507003c07acabb16375891d",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: daggerRepoURL,
						Commit:   "d44c734dbbbcecc75507003c07acabb16375891d",
					},
				},
				Hash: "d44c734dbbbcecc75507003c07acabb16375891d",
			},
		},
		{
			ref: "github.com/dagger/dagger//sdk/go:v0.9.3",
			res: &modules.Ref{
				Source: modules.Source{
					Git: &modules.GitSource{
						CloneURL: daggerRepoURL,
						Ref:      "sdk/go/v0.9.3",
						Commit:   "94120a6b0b70bfebad67a256beee8352dd7a67d7",
						Dir:      "sdk/go",
					},
				},
				Tag:  "v0.9.3",
				Hash: "94120a6b0b70bfebad67a256beee8352dd7a67d7",
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
