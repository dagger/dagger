package core

import (
	"context"
	"strings"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"dagger.io/dagger/engineconn"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestRestoredBindingAsksForSSHAgent covers a resumed agent bound to a tool
// whose recipe scoped the owner's SSH agent: a Query.git of an SSH remote,
// recorded with Host._sshAuthSocket, as a workspace snapshot of an SSH
// checkout is. The restored binding loads in a host-less client, which has
// no SSH agent of its own: rather than fail with a bare "SSH_AUTH_SOCK is not
// set", it asks the owner to lend theirs, naming the remote, never forever.
// The restorer here is an SDK client, which cannot prompt, so the ask fails,
// saying so.
func (AgentRestoreSuite) TestRestoredBindingAsksForSSHAgent(ctx context.Context, t *testctx.T) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	sshAuthSock, cleanup := setupPrivateRepoSSHAgent(t)
	defer cleanup()
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	connectWorkspace := func() (*dagger.Client, *agentTraceSink) {
		return connectWithTrace(ctx, t, engineconn.Config{
			Workdir:  workdir,
			ExtraEnv: []string{"SSH_AUTH_SOCK=" + sshAuthSock},
		})
	}

	const (
		remote       = "ssh://git@ssh-restore.invalid/org/repo.git"
		firstPrompt  = "hold the repository"
		firstReply   = "holding it"
		secondPrompt = "still holding it?"
		secondReply  = "still holding it"
	)
	source, sink := connectWorkspace()
	// Lazy: scoping the SSH agent records it, without reaching the remote.
	repoID, err := core.NewQuery(source).Git(remote).ID(ctx)
	require.NoError(t, err)
	model := cannedRecordingModel(ctx, t, source, core.NewQuery(source).LLM().
		WithPrompt(firstPrompt).
		WithResponse([]core.LLMContentBlockInput{{Kind: core.LLMContentBlockKindText, Text: firstReply}}).
		// Resumed, it uses the repository: its binding loads.
		WithPrompt(secondPrompt).
		WithResponse([]core.LLMContentBlockInput{{Kind: core.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "head"}}).
		WithToolResult("call_1", "", false).
		WithResponse([]core.LLMContentBlockInput{{Kind: core.LLMContentBlockKindText, Text: secondReply}}))
	holder := spawnAgent(ctx, t, source, spawnOpts{model: model, name: "ssh-holder", toolIDs: []core.ID{repoID}})
	_, reply, err := holder.sendAndWait(ctx, t, firstPrompt)
	require.NoError(t, err)
	require.Equal(t, firstReply, reply)

	sink.awaitRestorable(t, 1)
	require.NoError(t, source.Close())
	traces, logs := sink.capture()

	db := importCapture(ctx, t, traces, logs)
	plan := planByName(t, db.RestorePlan())
	target, _ := connectWorkspace()
	defer target.Close()
	restored := restoreAgent(ctx, t, target, db, plan["ssh-holder"])
	_, reply, err = restored.sendAndWait(ctx, t, secondPrompt)
	var report strings.Builder
	if err != nil {
		report.WriteString(err.Error() + "\n")
	}
	report.WriteString(reply + "\n")
	if out, err := restored.run(ctx, t, "state error"); err == nil {
		report.WriteString(out.Raw + "\n")
	}
	if transcript, _ := restored.snapshot(ctx, t); transcript != "" {
		report.WriteString(transcript)
	}
	got := report.String()
	require.Contains(t, got, `using your SSH keys for "`+remote+`" for a restored agent requires approval`,
		"the restored binding did not ask the owner for its SSH agent:\n%s", got)
	require.NotContains(t, got, "SSH_AUTH_SOCK is not set", got)
}
