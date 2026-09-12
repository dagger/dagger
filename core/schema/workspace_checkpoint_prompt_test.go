package schema

import (
	"context"
	"testing"

	gitsession "github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type checkpointPromptFake struct {
	prompt.PromptClient
	choice                 string
	err                    error
	selectCalls, boolCalls int
	approved               bool
}

func (f *checkpointPromptFake) PromptSelect(_ context.Context, r *prompt.SelectRequest, _ ...grpc.CallOption) (*prompt.SelectResponse, error) {
	f.selectCalls++
	if r.DefaultChoice != "cancel" {
		panic("unsafe default")
	}
	return &prompt.SelectResponse{Choice: f.choice}, f.err
}
func (f *checkpointPromptFake) PromptBool(context.Context, *prompt.BoolRequest, ...grpc.CallOption) (*prompt.BoolResponse, error) {
	f.boolCalls++
	return &prompt.BoolResponse{Response: f.approved}, nil
}

func TestCheckpointPromptChoices(t *testing.T) {
	for _, choice := range []string{checkpointInclude, checkpointDrop, checkpointCancel, "invalid"} {
		f := &checkpointPromptFake{choice: choice}
		got, err := checkpointPromptClient(t.Context(), f, true, "summary")
		if choice == "invalid" {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, choice, got)
		}
		require.Zero(t, f.boolCalls)
	}
	for _, supports := range []bool{false, true} {
		f := &checkpointPromptFake{err: status.Error(codes.Unimplemented, "old client"), approved: true}
		got, err := checkpointPromptClient(t.Context(), f, supports, "summary")
		require.NoError(t, err)
		require.Equal(t, checkpointInclude, got)
		require.Equal(t, 1, f.boolCalls)
	}
	f := &checkpointPromptFake{err: status.Error(codes.Canceled, "canceled")}
	_, err := checkpointPromptClient(t.Context(), f, true, "summary")
	require.Error(t, err)
	require.Zero(t, f.boolCalls)
}

func TestCheckpointDropPolicy(t *testing.T) {
	policy := &gitsession.CaptureGitPolicy{Include: []string{"*"}, Exclude: []string{"keep-exclusion"}, ApprovalTokens: []string{"previous"}}
	require.NoError(t, applyCheckpointDecision(policy, nil, checkpointDrop))
	require.True(t, policy.DropUntracked)
	require.Empty(t, policy.Include)
	require.Empty(t, policy.ApprovalTokens)
	require.Equal(t, []string{"keep-exclusion"}, policy.Exclude)
	require.Error(t, applyCheckpointDecision(policy, nil, checkpointCancel))
	require.Error(t, applyCheckpointDecision(policy, nil, "unknown"))
}

func TestCheckpointIncludeOmitsNestedRepositories(t *testing.T) {
	policy := &gitsession.CaptureGitPolicy{}
	candidates := []*gitsession.CaptureGitCandidate{
		{Path: "notes.txt", ApprovalToken: "tok-notes"},
		{Path: "wt/", Classification: gitsession.CaptureClassificationNestedRepository, ApprovalToken: "tok-wt"},
	}
	require.NoError(t, applyCheckpointDecision(policy, candidates, checkpointInclude))
	require.Equal(t, []string{"tok-notes"}, policy.ApprovalTokens, "a nested repository's state token must never be approved")
	require.Equal(t, []string{"wt/"}, policy.Exclude, "Include proceeds by excluding the nested boundary")
	require.False(t, policy.DropUntracked)
}

func TestCheckpointApprovalSummaryNamesNestedRepositories(t *testing.T) {
	summary := checkpointApprovalSummary([]*gitsession.CaptureGitCandidate{
		{Path: "notes.txt", Bytes: 8, ApprovalToken: "tok-notes"},
		{Path: "wt/", Classification: gitsession.CaptureClassificationNestedRepository, ApprovalToken: "tok-wt"},
	})
	require.Contains(t, summary, `"notes.txt"`)
	require.Contains(t, summary, `"wt/"`)
	require.Contains(t, summary, "nested repository")
	require.NotContains(t, summary, "tok-wt", "a boundary that is never captured has no reviewable state")
}
