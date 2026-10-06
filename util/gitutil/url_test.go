package gitutil

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactedRemote(t *testing.T) {
	for in, want := range map[string]string{
		"https://user:token@github.com/moby/buildkit.git#main": "https://github.com/moby/buildkit.git",
		"https://token@github.com/moby/buildkit":               "https://github.com/moby/buildkit",
		"https://github.com/moby/buildkit":                     "https://github.com/moby/buildkit",
		"ssh://git@github.com/moby/buildkit.git":               "ssh://github.com/moby/buildkit.git",
		"git@github.com:moby/buildkit.git#v1.0.0":              "github.com:moby/buildkit.git",
	} {
		u, err := ParseURL(in)
		require.NoError(t, err, in)
		require.Equal(t, want, u.RedactedRemote(), in)
	}
}

func TestParseURL(t *testing.T) {
	tests := []struct {
		url    string
		result GitURL
		err    bool
	}{
		{
			url: "http://github.com/moby/buildkit",
			result: GitURL{
				Scheme: HTTPProtocol,
				Host:   "github.com",
				Path:   "/moby/buildkit",
			},
		},
		{
			url: "https://github.com/moby/buildkit",
			result: GitURL{
				Scheme: HTTPSProtocol,
				Host:   "github.com",
				Path:   "/moby/buildkit",
			},
		},
		{
			url: "http://github.com/moby/buildkit#v1.0.0",
			result: GitURL{
				Scheme:   HTTPProtocol,
				Host:     "github.com",
				Path:     "/moby/buildkit",
				Fragment: &GitURLFragment{Ref: "v1.0.0"},
			},
		},
		{
			url: "https://github.com/moby/buildkit@v1.0.0",
			result: GitURL{
				Scheme:   HTTPSProtocol,
				Host:     "github.com",
				Path:     "/moby/buildkit",
				Fragment: &GitURLFragment{Ref: "v1.0.0"},
			},
		},
		{
			url: "http://github.com/moby/buildkit#v1.0.0:subdir",
			result: GitURL{
				Scheme:   HTTPProtocol,
				Host:     "github.com",
				Path:     "/moby/buildkit",
				Fragment: &GitURLFragment{Ref: "v1.0.0", Subdir: "subdir"},
			},
		},
		{
			url: "http://foo:bar@github.com/moby/buildkit#v1.0.0",
			result: GitURL{
				Scheme:   HTTPProtocol,
				Host:     "github.com",
				Path:     "/moby/buildkit",
				Fragment: &GitURLFragment{Ref: "v1.0.0"},
				User:     url.UserPassword("foo", "bar"),
			},
		},
		{
			url: "ssh://git@github.com/moby/buildkit.git",
			result: GitURL{
				Scheme: SSHProtocol,
				Host:   "github.com",
				Path:   "/moby/buildkit.git",
				User:   url.User("git"),
			},
		},
		{
			url: "ssh://git@github.com:22/moby/buildkit.git",
			result: GitURL{
				Scheme: SSHProtocol,
				Host:   "github.com:22",
				Path:   "/moby/buildkit.git",
				User:   url.User("git"),
			},
		},
		{
			url: "git@github.com:moby/buildkit.git",
			result: GitURL{
				Scheme:   SSHProtocol,
				Host:     "github.com",
				Path:     "moby/buildkit.git",
				User:     url.User("git"),
				scpStyle: true,
			},
		},
		{
			url: "git@github.com:moby/buildkit.git#v1.0.0",
			result: GitURL{
				Scheme:   SSHProtocol,
				Host:     "github.com",
				Path:     "moby/buildkit.git",
				Fragment: &GitURLFragment{Ref: "v1.0.0"},
				User:     url.User("git"),
				scpStyle: true,
			},
		},
		{
			url: "git@github.com:moby/buildkit.git@v1.0.0",
			result: GitURL{
				Scheme:   SSHProtocol,
				Host:     "github.com",
				Path:     "moby/buildkit.git",
				Fragment: &GitURLFragment{Ref: "v1.0.0"},
				User:     url.User("git"),
				scpStyle: true,
			},
		},
		{
			url: "git@github.com:moby/buildkit.git#v1.0.0:hack",
			result: GitURL{
				Scheme:   SSHProtocol,
				Host:     "github.com",
				Path:     "moby/buildkit.git",
				Fragment: &GitURLFragment{Ref: "v1.0.0", Subdir: "hack"},
				User:     url.User("git"),
				scpStyle: true,
			},
		},
		{
			url: "nonstandarduser@example.com:/srv/repos/weird/project.git",
			result: GitURL{
				Scheme:   SSHProtocol,
				Host:     "example.com",
				Path:     "/srv/repos/weird/project.git",
				User:     url.User("nonstandarduser"),
				scpStyle: true,
			},
		},
		{
			url: "ssh://root@subdomain.example.hostname:2222/root/my/really/weird/path/foo.git",
			result: GitURL{
				Scheme: SSHProtocol,
				Host:   "subdomain.example.hostname:2222",
				Path:   "/root/my/really/weird/path/foo.git",
				User:   url.User("root"),
			},
		},
		{
			url: "git://host.xz:1234/path/to/repo.git",
			result: GitURL{
				Scheme: GitProtocol,
				Host:   "host.xz:1234",
				Path:   "/path/to/repo.git",
			},
		},
		{
			url: "ssh://someuser@192.168.0.123:456/~/repo-in-my-home-dir.git",
			result: GitURL{
				Scheme: SSHProtocol,
				Host:   "192.168.0.123:456",
				Path:   "/~/repo-in-my-home-dir.git",
				User:   url.User("someuser"),
			},
		},
		{
			url: "httpx://github.com/moby/buildkit",
			err: true,
		},
		{
			url: "HTTP://github.com/moby/buildkit",
			result: GitURL{
				Scheme: HTTPProtocol,
				Host:   "github.com",
				Path:   "/moby/buildkit",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.url, func(t *testing.T) {
			remote, err := ParseURL(test.url)
			if test.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, test.result.String(), remote.String())
				require.Equal(t, test.result.Scheme, remote.Scheme)
				require.Equal(t, test.result.Host, remote.Host)
				require.Equal(t, test.result.Path, remote.Path)
				require.Equal(t, test.result.Fragment, remote.Fragment)
				require.Equal(t, test.result.User.String(), remote.User.String())
			}
		})
	}
}

func TestSameRepository(t *testing.T) {
	const repo = "git@github.com:dagger/dagger.io"
	for _, tc := range []struct {
		other string
		same  bool
	}{
		{"git@github.com:dagger/dagger.io.git", true},
		{"https://github.com/dagger/dagger.io", true},
		{"https://GitHub.com/dagger/dagger.io/", true},
		{"ssh://git@github.com/dagger/dagger.io.git", true},
		{"ssh://git@github.com:22/dagger/dagger.io", true},
		{"https://github.com/dagger/dagger.io#main:docs", true},
		{"ssh://git@github.com:2222/dagger/dagger.io", false},
		{"https://github.com/dagger/dagger", false},
		{"https://github.com/vito/dagger.io", false},
		{"https://gitlab.com/dagger/dagger.io", false},
	} {
		t.Run(tc.other, func(t *testing.T) {
			a, err := ParseURL(repo)
			require.NoError(t, err)
			b, err := ParseURL(tc.other)
			require.NoError(t, err)
			require.Equal(t, tc.same, SameRepository(a, b))
			require.Equal(t, tc.same, SameRepository(b, a))
		})
	}
	require.False(t, SameRepository(nil, nil))
}

func TestSameRepositoryPathCase(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		// GitHub and GitLab route paths case-insensitively.
		{"https://github.com/dagger/dagger.io", "https://github.com/Dagger/Dagger.io.git", true},
		{"git@github.com:dagger/dagger.io", "https://GitHub.com/DAGGER/dagger.io", true},
		{"https://gitlab.com/group/project", "git@gitlab.com:Group/Project.git", true},
		{"https://github.com/dagger/dagger.io", "https://github.com/Dagger/dagger", false},
		// Elsewhere, differently cased paths may be different repositories.
		{"https://git.example.com/org/repo", "https://git.example.com/Org/Repo", false},
		{"https://bitbucket.org/org/repo", "https://bitbucket.org/Org/repo", false},
		{"https://git.example.com/org/repo", "https://git.example.com/org/repo.git", true},
	} {
		t.Run(tc.a+" "+tc.b, func(t *testing.T) {
			a, err := ParseURL(tc.a)
			require.NoError(t, err)
			b, err := ParseURL(tc.b)
			require.NoError(t, err)
			require.Equal(t, tc.same, SameRepository(a, b))
			require.Equal(t, tc.same, SameRepository(b, a))
		})
	}
}

func TestParseCloneURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantSchemes []string
		wantErr     bool
	}{
		{
			name:        "https URL is single candidate",
			url:         "https://github.com/foo/bar",
			wantSchemes: []string{HTTPSProtocol},
		},
		{
			name:        "ssh URL is single candidate",
			url:         "ssh://git@github.com/foo/bar.git",
			wantSchemes: []string{SSHProtocol},
		},
		{
			name:        "scp-style URL is single candidate",
			url:         "git@github.com:foo/bar.git",
			wantSchemes: []string{SSHProtocol},
		},
		{
			name:        "schemeless URL yields https then ssh candidates",
			url:         "github.com/foo/bar",
			wantSchemes: []string{HTTPSProtocol, SSHProtocol},
		},
		{
			name:        "schemeless URL with subpath yields https then ssh candidates",
			url:         "github.com/foo/bar/subdir",
			wantSchemes: []string{HTTPSProtocol, SSHProtocol},
		},
		{
			name:    "unsupported protocol propagates error",
			url:     "httpx://github.com/foo/bar",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates, err := ParseCloneURL(test.url)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, candidates, len(test.wantSchemes))
			for i, want := range test.wantSchemes {
				require.Equal(t, want, candidates[i].Scheme, "candidate %d scheme", i)
			}
		})
	}
}
