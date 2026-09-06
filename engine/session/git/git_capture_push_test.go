package git

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCaptureGitPushURLs(t *testing.T) {
	skipIfNoGit(t)
	const configuredURL = "https://github.com/vito/agents"
	for _, tc := range []struct {
		name   string
		config [][2]string
		want   []string
	}{
		{name: "same as fetch"},
		{
			name:   "pushInsteadOf applies before fetch rewrite",
			config: [][2]string{{"url.git@github.com:.pushInsteadOf", "https://github.com/"}},
			want:   []string{"git@github.com:vito/agents"},
		},
		{
			name: "longest pushInsteadOf wins",
			config: [][2]string{
				{"url.ssh://git@fallback.test/.pushInsteadOf", "https://github.com/"},
				{"url.ssh://git@push.test/.pushInsteadOf", "https://github.com/vito/"},
			},
			want: []string{"ssh://git@push.test/agents"},
		},
		{
			name: "pushurl overrides pushInsteadOf",
			config: [][2]string{
				{"url.git@github.com:.pushInsteadOf", "https://github.com/"},
				{"remote.origin.pushurl", "git://push.test/agents"},
			},
			want: []string{"git://push.test/agents"},
		},
		{
			name: "insteadOf applies to explicit pushurl",
			config: [][2]string{
				{"remote.origin.pushurl", "push:agents"},
				{"url.ssh://git@push.test/.insteadOf", "push:"},
			},
			want: []string{"ssh://git@push.test/agents"},
		},
		{
			name:   "strip HTTP credentials and query",
			config: [][2]string{{"remote.origin.pushurl", "https://user:password@push.test/agents?token=secret#fragment"}},
			want:   []string{"https://push.test/agents"},
		},
		{
			name:   "preserve SSH user but not password",
			config: [][2]string{{"remote.origin.pushurl", "ssh://git:password@push.test/agents"}},
			want:   []string{"ssh://git@push.test/agents"},
		},
		{
			name: "retain all destinations",
			config: [][2]string{
				{"remote.origin.pushurl", "git://first.test/agents"},
				{"remote.origin.pushurl", "git://second.test/agents"},
			},
			want: []string{"git://first.test/agents", "git://second.test/agents"},
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
			require.Equal(t, tc.want, meta.RemotePushUrls)
		})
	}
}

func TestCaptureGitRejectsChangedPushURLs(t *testing.T) {
	skipIfNoGit(t)
	repo, home, _ := initCaptureRepo(t)
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")
	remote, err := selectCaptureRemote(t.Context(), repo, head)
	require.NoError(t, err)
	remote.pushURLs, err = capturePushURLs(t.Context(), repo, remote.name, remote.sanitizedURL)
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
	require.Empty(t, meta.RemotePushUrls)
}
