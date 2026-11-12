package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/engineutil"
	gitsession "github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestReconstructGitRemoteMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		pack engineutil.GitCheckoutPack
		want []GitRemote
	}{
		{
			name: "all remotes without origin",
			pack: engineutil.GitCheckoutPack{HasRemoteMetadata: true, UpstreamRemote: "trunk", Remotes: []*gitsession.CheckoutRemote{
				{Name: "fork", Url: "https://example.com/fork"},
				{Name: "trunk", Url: "https://example.com/trunk", PushUrl: "ssh://git@example.com/trunk"},
			}},
			want: []GitRemote{{Name: "fork", URL: "https://example.com/fork"}, {Name: "trunk", URL: "https://example.com/trunk", PushURL: "ssh://git@example.com/trunk"}},
		},
		{name: "captured empty list", pack: engineutil.GitCheckoutPack{HasRemoteMetadata: true}},
		{name: "older client", want: []GitRemote{{Name: "origin", URL: "https://example.com/origin"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, reconstructGitDir(t.Context(), dir, &tc.pack, "https://example.com/origin"))
			git := gitutil.NewGitCLI(gitutil.WithDir(dir))
			remotes, err := readGitConfigRemotes(t.Context(), git)
			require.NoError(t, err)
			if len(tc.want) == 0 {
				require.Empty(t, remotes)
			} else {
				require.Equal(t, tc.want, remotes)
			}
			if tc.pack.HasRemoteMetadata {
				out, err := git.Run(t.Context(), "config", "--get", "dagger.remotes")
				require.NoError(t, err)
				var captured []GitRemote
				require.NoError(t, json.Unmarshal(out, &captured))
				require.Equal(t, tc.want, MergeGitRemotes(nil, captured))
			}
		})
	}
}

// TestReconstructGitDirIncrementalMatchesFull checks that bringing an earlier
// reconstruction of a checkout to a later state, from only what the checkout
// gained, gives the same repository as reconstructing the later state from
// scratch: refs, HEAD, index and configuration.
func TestReconstructGitDirIncrementalMatchesFull(t *testing.T) {
	ctx := t.Context()
	host := t.TempDir()
	home := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+home,
			"GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@a", "GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@a")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	commit := func(name, content string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(host, name), []byte(content), 0o644))
		git(host, "add", name)
		git(host, "commit", "-qm", name)
	}
	// pack packs the host checkout's current state, relative to the ref
	// targets of the reconstruction at base when one is given.
	pack := func(base string) *engineutil.GitCheckoutPack {
		t.Helper()
		p := &engineutil.GitCheckoutPack{
			HeadSHA:           git(host, "rev-parse", "HEAD"),
			HasRemoteMetadata: true,
			Remotes:           []*gitsession.CheckoutRemote{{Name: "origin", Url: git(host, "remote", "get-url", "origin")}},
			UpstreamRemote:    "origin",
		}
		if ref, err := exec.CommandContext(ctx, "git", "-C", host, "symbolic-ref", "-q", "HEAD").Output(); err == nil {
			p.HeadRef = strings.TrimSpace(string(ref))
		}
		args := []string{"--branches", "--tags", "HEAD"}
		if base != "" {
			p.RelativeToHaves = true
			for _, line := range strings.Split(git(host, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/tags"), "\n") {
				name, sha, _ := strings.Cut(line, " ")
				p.Refs = append(p.Refs, &gitsession.CheckoutRef{Name: name, Sha: sha})
			}
			haves := strings.Fields(git(base, "for-each-ref", "--format=%(objectname)"))
			haves = append(haves, git(base, "rev-parse", "HEAD"))
			missing := git(host, append([]string{"rev-list", "--objects"}, append(args, append([]string{"--not"}, haves...)...)...)...)
			if missing == "" {
				return p
			}
			args = append(args, append([]string{"--not"}, haves...)...)
		}
		p.BundlePath = filepath.Join(t.TempDir(), "checkout.bundle")
		git(host, append([]string{"bundle", "create", "-q", p.BundlePath}, args...)...)
		return p
	}
	describe := func(dir string) map[string]string {
		t.Helper()
		git(dir, "fsck", "--connectivity-only", "--no-dangling")
		head, err := exec.CommandContext(ctx, "git", "-C", dir, "symbolic-ref", "-q", "HEAD").Output()
		if err != nil {
			head = []byte("detached")
		}
		config := strings.Split(git(dir, "config", "--local", "--list"), "\n")
		slices.Sort(config)
		return map[string]string{
			"refs":   git(dir, "for-each-ref", "--format=%(refname) %(objectname)"),
			"head":   strings.TrimSpace(string(head)) + " " + git(dir, "rev-parse", "HEAD"),
			"index":  git(dir, "ls-files", "--stage"),
			"config": strings.Join(config, "\n"),
		}
	}
	// step reconstructs the host's current state both ways and compares.
	step := func(name, base string) string {
		t.Helper()
		incremental := t.TempDir()
		require.NoError(t, exec.CommandContext(ctx, "cp", "-a", base+"/.", incremental).Run())
		p := pack(base)
		require.NoError(t, reconstructGitDirIncremental(ctx, incremental, p, ""), name)
		full := t.TempDir()
		require.NoError(t, reconstructGitDir(ctx, full, pack(""), ""), name)
		require.Equal(t, describe(full), describe(incremental), name)
		return incremental
	}

	git(host, "init", "-q", "-b", "main")
	git(host, "remote", "add", "origin", "https://example.com/one")
	commit("a.txt", "one")
	git(host, "tag", "-a", "v1", "-m", "v1")
	git(host, "branch", "feature")
	base := t.TempDir()
	require.NoError(t, reconstructGitDir(ctx, base, pack(""), ""))

	// New commits, a deleted branch, a new tag and a changed remote.
	commit("b.txt", "two")
	git(host, "branch", "-D", "feature")
	git(host, "tag", "v2")
	git(host, "remote", "set-url", "origin", "https://example.com/two")
	base = step("new commits", base)

	// Nothing new to send: only a branch moves to a commit already there.
	git(host, "branch", "topic", "v1")
	base = step("refs only", base)

	// A branch replaced by one under its name.
	git(host, "branch", "-D", "topic")
	git(host, "branch", "topic/sub", "v1")
	base = step("namespace", base)

	// A detached HEAD on a new commit.
	git(host, "checkout", "-q", "--detach")
	commit("c.txt", "three")
	step("detached", base)
}
