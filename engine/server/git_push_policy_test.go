package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/stretchr/testify/require"
)

func TestGitPushNeedsRefApproval(t *testing.T) {
	trunk := gitRemoteDefault{reached: true, branch: "refs/heads/trunk"}
	unknown := gitRemoteDefault{reached: true}
	unreachable := gitRemoteDefault{}
	for _, tc := range []struct {
		name   string
		ref    string
		force  bool
		remote gitRemoteDefault
		asks   bool
	}{
		{"feature branch", "refs/heads/feature/x", false, trunk, false},
		{"force to a feature branch", "refs/heads/feature/x", true, trunk, gitForcePushToOtherBranchesAsks},
		{"default branch", "refs/heads/trunk", false, trunk, true},
		{"default branch, as stored case-insensitively", "refs/heads/Trunk", false, trunk, true},
		{"force to the default branch", "refs/heads/trunk", true, trunk, true},
		{"main though not the default", "refs/heads/main", false, trunk, true},
		{"master though not the default", "refs/heads/master", false, trunk, true},
		{"MAIN", "refs/heads/MAIN", false, trunk, true},
		{"tag", "refs/tags/v1.0.0", false, trunk, true},
		{"other ref", "refs/notes/commits", false, trunk, true},
		{"unknown default, feature branch", "refs/heads/feature/x", false, unknown, false},
		{"unknown default, main", "refs/heads/main", false, unknown, true},
		{"unknown default, master", "refs/heads/master", false, unknown, true},
		{"unreachable, feature branch", "refs/heads/feature/x", false, unreachable, true},
		{"unreachable, tag", "refs/tags/v1.0.0", false, unreachable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.asks, gitPushNeedsRefApproval(tc.ref, tc.force, tc.remote))
		})
	}
	require.False(t, gitForcePushToOtherBranchesAsks, "rebasing a pull request's branch is routine")
}

// remoteHeads is a fake core.GitRemoteHead answering from a table, counting
// the questions.
type remoteHeads struct {
	heads map[string]string
	err   error
	asked atomic.Int32
}

func (r *remoteHeads) probe(_ context.Context, remote string) (string, error) {
	r.asked.Add(1)
	if r.err != nil {
		return "", r.err
	}
	return r.heads[remote], nil
}

func TestGitPushApprovalDefaultBranch(t *testing.T) {
	questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision {
		return prompt.APPROVAL_ALLOW_SESSION
	}}
	s := newGitPushTestSession(t, questions, &pushRoutingServer{})
	const remote = "git@example.com:repo"
	heads := &remoteHeads{heads: map[string]string{remote: "refs/heads/trunk"}}
	s.srv.gitRemoteHead = heads.probe
	push := func(ref string, force bool) {
		t.Helper()
		md, err := s.srv.AuthorizeGitPush(s.moduleCtx, remote, ref, force, false)
		require.NoError(t, err)
		require.Equal(t, s.owner.clientMetadata, md.Owner)
	}
	const by = ` (requested by module "pusher")?`

	// The first push to a branch other than the default asks about the
	// remote, once for the session; force-pushes there need nothing more.
	push("refs/heads/feature", false)
	require.Len(t, questions.asked(), 1)
	req := questions.asked()[0]
	require.Equal(t, approvalKindGitPushRemote, req.Kind)
	require.Equal(t, remote, req.Subject)
	require.Equal(t, "Allow pushes to non-default branches of git@example.com:repo"+by, req.Prompt)
	require.False(t, req.Danger)
	push("refs/heads/other", false)
	push("refs/heads/feature", true)
	require.Len(t, questions.asked(), 1)

	// The default branch asks about the exact ref, and force with danger.
	push("refs/heads/trunk", false)
	require.Len(t, questions.asked(), 2)
	require.Equal(t, approvalKindGitPush, questions.asked()[1].Kind)
	require.Equal(t, "Allow push to git@example.com:repo @ refs/heads/trunk"+by, questions.asked()[1].Prompt)
	push("refs/heads/trunk", true)
	require.Len(t, questions.asked(), 3)
	require.Equal(t, approvalKindGitForcePush, questions.asked()[2].Kind)
	require.True(t, questions.asked()[2].Danger)

	// So do main and master, whatever the remote says, and tags.
	for _, ref := range []string{"refs/heads/main", "refs/heads/master", "refs/tags/v1.0.0"} {
		before := len(questions.asked())
		push(ref, false)
		require.Len(t, questions.asked(), before+1)
		require.Equal(t, approvalKindGitPush, questions.asked()[before].Kind)
		require.Equal(t, remote+" @ "+ref, questions.asked()[before].Subject)
	}
	require.EqualValues(t, 1, heads.asked.Load(), "the default branch is asked once per session")

	// A push the owner makes itself never asks, nor probes the remote.
	_, err := s.srv.AuthorizeGitPush(s.ownerCtx, "https://example.com/elsewhere", "refs/heads/trunk", true, false)
	require.NoError(t, err)
	require.EqualValues(t, 1, heads.asked.Load())
}

func TestGitPushApprovalRemoteDenied(t *testing.T) {
	questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision {
		return prompt.APPROVAL_DENY
	}}
	s := newGitPushTestSession(t, questions, &pushRoutingServer{})
	heads := &remoteHeads{heads: map[string]string{"https://example.com/repo": "refs/heads/main"}}
	s.srv.gitRemoteHead = heads.probe
	for range 2 {
		md, err := s.srv.AuthorizeGitPush(s.moduleCtx, "https://example.com/repo", "refs/heads/feature", false, false)
		require.ErrorContains(t, err, "permission denied")
		require.Nil(t, md)
	}
	require.Len(t, questions.asked(), 2, "a denial is not remembered")
	require.Equal(t, approvalKindGitPushRemote, questions.asked()[1].Kind)
}

func TestGitPushApprovalUnknownDefaultBranch(t *testing.T) {
	questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision {
		return prompt.APPROVAL_ALLOW_ONCE
	}}
	s := newGitPushTestSession(t, questions, &pushRoutingServer{})
	const remote = "https://example.com/private"
	kinds := func() []string {
		var kinds []string
		for _, req := range questions.asked() {
			kinds = append(kinds, req.Kind+" "+req.Subject)
		}
		return kinds
	}

	// A remote that cannot be reached asks about every ref, as before, and
	// is asked again by the next push.
	heads := &remoteHeads{err: core.ErrGitRemoteUnreachable}
	s.srv.gitRemoteHead = heads.probe
	for range 2 {
		_, err := s.srv.AuthorizeGitPush(s.moduleCtx, remote, "refs/heads/feature", false, false)
		require.NoError(t, err)
	}
	require.Equal(t, []string{
		"git-push " + remote + " @ refs/heads/feature",
		"git-push " + remote + " @ refs/heads/feature",
	}, kinds())
	require.EqualValues(t, 2, heads.asked.Load(), "an unreachable remote is not remembered")

	// One that answers without naming its default branch (e.g. it requires
	// authentication) asks about main and master only, and is remembered.
	heads = &remoteHeads{heads: map[string]string{}}
	s.srv.gitRemoteHead = heads.probe
	for _, ref := range []string{"refs/heads/feature", "refs/heads/main", "refs/heads/master"} {
		_, err := s.srv.AuthorizeGitPush(s.moduleCtx, remote, ref, false, false)
		require.NoError(t, err)
	}
	require.Equal(t, []string{
		"git-push " + remote + " @ refs/heads/feature",
		"git-push " + remote + " @ refs/heads/feature",
		"git-push-remote " + remote,
		"git-push " + remote + " @ refs/heads/main",
		"git-push " + remote + " @ refs/heads/master",
	}, kinds())
	_, err := s.srv.AuthorizeGitPush(s.moduleCtx, remote, "refs/heads/feature", false, false)
	require.NoError(t, err)
	require.EqualValues(t, 1, heads.asked.Load(), "an answer is remembered")
	require.Len(t, questions.asked(), 6, "allow once asks again")
}

func TestGitPushApprovalOwnerOrigin(t *testing.T) {
	questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision {
		return prompt.APPROVAL_ALLOW_ONCE
	}}
	const root = "/home/me/repo"
	routing := &pushRoutingServer{checkouts: map[string][]*git.GitConfigEntry{root: {
		{Key: "remote.origin.url", Value: "https://example.com/me/repo.git"},
		{Key: "url.git@push.example.com:.pushinsteadof", Value: "https://example.com/"},
	}}}
	s := newGitPushTestSession(t, questions, routing)
	ws := &core.Workspace{}
	ws.SetHostPath(root)
	s.owner.workspace = ws
	heads := &remoteHeads{heads: map[string]string{}}
	s.srv.gitRemoteHead = heads.probe

	// The origin as configured, and as the owner's rewrites resolve it for a
	// push, are approved already for branches other than the default.
	for _, remote := range []string{"https://example.com/me/repo", "git@example.com:me/repo", "git@push.example.com:me/repo.git"} {
		_, err := s.srv.AuthorizeGitPush(s.moduleCtx, remote, "refs/heads/feature", true, false)
		require.NoError(t, err)
	}
	require.Empty(t, questions.asked())
	require.Equal(t, 1, routing.checkoutRequests, "the origin is read once per session")

	// Its default branch still asks, and so does any other repository.
	_, err := s.srv.AuthorizeGitPush(s.moduleCtx, "https://example.com/me/repo", "refs/heads/main", false, false)
	require.NoError(t, err)
	_, err = s.srv.AuthorizeGitPush(s.moduleCtx, "https://example.com/me/other", "refs/heads/feature", false, false)
	require.NoError(t, err)
	_, err = s.srv.AuthorizeGitPush(s.moduleCtx, "https://evil.example/me/repo", "refs/heads/feature", false, false)
	require.NoError(t, err)
	var asked []string
	for _, req := range questions.asked() {
		asked = append(asked, req.Kind+" "+req.Subject)
	}
	require.Equal(t, []string{
		"git-push https://example.com/me/repo @ refs/heads/main",
		"git-push-remote https://example.com/me/other",
		"git-push-remote https://evil.example/me/repo",
	}, asked)
}

func TestGitRemoteDefaultsShareInFlight(t *testing.T) {
	var d gitRemoteDefaults
	key := gitPushPolicyKey{owner: "owner", remote: "https://example.com/repo"}
	release := make(chan struct{})
	var probes atomic.Int32
	probe := func(context.Context) gitRemoteDefault {
		probes.Add(1)
		<-release
		return gitRemoteDefault{reached: true, branch: "refs/heads/main"}
	}
	results := make(chan gitRemoteDefault, 3)
	for range 3 {
		go func() { results <- d.get(t.Context(), key, probe) }()
	}
	// A caller giving up gets nothing, without failing the probe in flight.
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.Eventually(t, func() bool { return probes.Load() == 1 }, 10*time.Second, 10*time.Millisecond)
	require.Equal(t, gitRemoteDefault{}, d.get(canceled, key, probe))
	close(release)
	for range 3 {
		require.Equal(t, gitRemoteDefault{reached: true, branch: "refs/heads/main"}, <-results)
	}
	require.EqualValues(t, 1, probes.Load())
	// Another owner asks for itself.
	d.get(t.Context(), gitPushPolicyKey{owner: "other", remote: key.remote}, probe)
	require.EqualValues(t, 2, probes.Load())
}
