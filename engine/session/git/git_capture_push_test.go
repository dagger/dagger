package git

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolvePushURL(t *testing.T) {
	skipIfNoGit(t)
	const remote = "https://github.com/dagger/dagger"
	require.Equal(t, remote, ResolvePushURL(remote, []*GitConfigEntry{
		nil,
		{Key: "url.insteadof", Value: remote},
		{Key: "url.pushinsteadof", Value: remote},
		{Key: "url..insteadof", Value: remote},
		{Key: "url..pushinsteadof", Value: remote},
	}))
	for _, tc := range []struct {
		name    string
		entries []*GitConfigEntry
		want    string
	}{
		{name: "unchanged", want: remote},
		{name: "push instead of fetch", entries: []*GitConfigEntry{
			{Key: "url.ssh://git@push.test/.pushinsteadof", Value: "https://github.com/"},
			{Key: "url.ssh://git@fetch.test/.insteadof", Value: "https://github.com/"},
		}, want: "ssh://git@push.test/dagger/dagger"},
		{name: "longest and first tie", entries: []*GitConfigEntry{
			{Key: "url.ssh://git@fallback.test/.pushinsteadof", Value: "https://github.com/"},
			{Key: "url.git@push.test:.pushinsteadof", Value: "https://github.com/dagger/"},
			{Key: "url.git@other.test:.pushinsteadof", Value: "https://github.com/dagger/"},
		}, want: "git@push.test:dagger"},
		{name: "no recursive rewriting", entries: []*GitConfigEntry{
			{Key: "url.alias:.pushinsteadof", Value: "https://github.com/"},
			{Key: "url.ssh://git@push.test/Case/.insteadof", Value: "alias:"},
		}, want: "alias:dagger/dagger"},
		{name: "insteadOf only", entries: []*GitConfigEntry{
			{Key: "url.git@push.test:.insteadof", Value: "https://github.com/"},
		}, want: "git@push.test:dagger/dagger"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, home := initRepo(t, "main")
			gitCmd(t, home, repo, "remote", "add", "origin", remote)
			var raw string
			for _, entry := range tc.entries {
				gitCmd(t, home, repo, "config", "--add", entry.Key, entry.Value)
				raw += entry.Key + "\n" + entry.Value + "\x00"
			}
			filtered, err := parseGitConfigOutput([]byte(raw))
			require.NoError(t, err)
			require.ElementsMatch(t, tc.entries, filtered.Entries)
			require.Equal(t, tc.want, ResolvePushURL(remote, filtered.Entries))
			require.Equal(t, gitCmd(t, home, repo, "remote", "get-url", "--push", "origin"), ResolvePushURL(remote, filtered.Entries))
		})
	}
}

func TestCaptureGitPushURL(t *testing.T) {
	skipIfNoGit(t)
	const configuredURL = "https://github.com/vito/agents"
	for _, tc := range []struct {
		name   string
		config [][2]string
		want   string
	}{
		{name: "same as fetch"},
		{
			name:   "pushInsteadOf applies before fetch rewrite",
			config: [][2]string{{"url.git@github.com:.pushInsteadOf", "https://github.com/"}},
			want:   "git@github.com:vito/agents",
		},
		{
			name: "longest pushInsteadOf wins",
			config: [][2]string{
				{"url.ssh://git@fallback.test/.pushInsteadOf", "https://github.com/"},
				{"url.ssh://git@push.test/.pushInsteadOf", "https://github.com/vito/"},
			},
			want: "ssh://git@push.test/agents",
		},
		{
			name: "pushurl overrides pushInsteadOf",
			config: [][2]string{
				{"url.git@github.com:.pushInsteadOf", "https://github.com/"},
				{"remote.origin.pushurl", "git://push.test/agents"},
			},
			want: "git://push.test/agents",
		},
		{
			name: "insteadOf applies to explicit pushurl",
			config: [][2]string{
				{"remote.origin.pushurl", "push:agents"},
				{"url.ssh://git@push.test/.insteadOf", "push:"},
			},
			want: "ssh://git@push.test/agents",
		},
		{
			name:   "strip HTTP credentials and query",
			config: [][2]string{{"remote.origin.pushurl", "https://user:password@push.test/agents?token=secret#fragment"}},
			want:   "https://push.test/agents",
		},
		{
			name:   "preserve SSH user but not password",
			config: [][2]string{{"remote.origin.pushurl", "ssh://git:password@push.test/agents"}},
			want:   "ssh://git@push.test/agents",
		},
		{
			name: "first destination wins",
			config: [][2]string{
				{"remote.origin.pushurl", "git://first.test/agents"},
				{"remote.origin.pushurl", "git://second.test/agents"},
			},
			want: "git://first.test/agents",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, home, remote := initCaptureRepo(t)
			// Only fetches reach a real (local) remote. Capturing push metadata
			// must not contact any of the synthetic push destinations.
			gitCmd(t, home, repo, "remote", "set-url", "origin", configuredURL)
			gitCmd(t, home, repo, "config", "url."+remote+".insteadOf", configuredURL)
			for _, entry := range tc.config {
				gitCmd(t, home, repo, "config", "--add", entry[0], entry[1])
			}
			meta := captureGit(t, repo, &CaptureGitPolicy{}).metadata(t)
			require.Nil(t, meta.Error)
			require.Equal(t, remote, meta.RemoteUrl)
			require.Equal(t, tc.want, meta.RemotePushUrl)
		})
	}
}

func TestCaptureGitRejectsChangedPushURL(t *testing.T) {
	skipIfNoGit(t)
	repo, home, _ := initCaptureRepo(t)
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")
	remote, err := selectCaptureRemote(t.Context(), repo, head)
	require.NoError(t, err)
	remote.pushURL, err = capturePushURL(t.Context(), repo, remote.name, remote.sanitizedURL)
	require.NoError(t, err)
	require.NoError(t, revalidateCaptureRemote(t.Context(), repo, remote))
	gitCmd(t, home, repo, "config", "remote.origin.pushurl", "git://different.test/agents")
	require.ErrorContains(t, revalidateCaptureRemote(t.Context(), repo, remote), "push destinations changed")
}

func TestCaptureGitRejectsMalformedPushURL(t *testing.T) {
	skipIfNoGit(t)
	repo, home, _ := initCaptureRepo(t)
	gitCmd(t, home, repo, "config", "remote.origin.pushurl", "https://user:do-not-leak@bad%host/repo")
	meta := captureGit(t, repo, &CaptureGitPolicy{}).metadata(t)
	require.NotNil(t, meta.Error)
	require.Contains(t, meta.Error.Message, "invalid push URL")
	require.NotContains(t, meta.Error.Message, "do-not-leak")
	require.Empty(t, meta.RemotePushUrl)
}
