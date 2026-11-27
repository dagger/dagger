package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	gitsession "github.com/dagger/dagger/engine/session/git"
	"github.com/stretchr/testify/require"
)

func TestCheckpointPromptChoices(t *testing.T) {
	req := checkpointPromptRequest("summary")
	require.NotNil(t, req.Select)
	require.Nil(t, req.Boolean)
	require.Equal(t, checkpointCancel, req.Select.Default, "never implicitly include")
	require.Equal(t, &[2]string{checkpointInclude, checkpointCancel}, req.Select.BoolFallback,
		"an old client's yes includes, its no cancels")
	var ids []string
	for _, c := range req.Select.Choices {
		ids = append(ids, c.ID)
	}
	require.Equal(t, []string{checkpointInclude, checkpointDrop, checkpointCancel}, ids)
	require.Contains(t, req.Message, "summary")

	for _, choice := range []string{checkpointInclude, checkpointDrop, checkpointCancel, "invalid"} {
		got, err := checkpointChoice(&core.PromptAnswer{Choice: choice})
		if choice == "invalid" {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, choice, got)
		}
	}
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
