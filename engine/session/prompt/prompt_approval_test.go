package prompt

import (
	"context"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// boolHandler is a frontend that can only ask yes/no, like plain and dots.
type boolHandler struct {
	answer  bool
	prompts []string
}

func (h *boolHandler) HandlePrompt(_ context.Context, _, prompt string, dest any) error {
	h.prompts = append(h.prompts, prompt)
	*dest.(*bool) = h.answer
	return nil
}

func (h *boolHandler) HandleForm(context.Context, *huh.Form) error { panic("not a form") }

type approvalHandler struct {
	boolHandler
	decision ApprovalDecision
}

func (h *approvalHandler) HandleApproval(context.Context, *ApprovalRequest) (ApprovalDecision, error) {
	return h.decision, nil
}

func TestPromptApprovalYesNo(t *testing.T) {
	// No persistent key: these tests must not touch the user's remembered
	// approvals.
	req := &ApprovalRequest{Kind: "git-push", Prompt: "Allow push to x @ y?"}
	for _, tc := range []struct {
		answer bool
		want   ApprovalDecision
	}{
		// A yes means what it meant before approvals had a grant scope.
		{true, APPROVAL_ALLOW_SESSION},
		{false, APPROVAL_DENY},
	} {
		handler := &boolHandler{answer: tc.answer}
		res, err := NewPromptAttachable(handler).PromptApproval(t.Context(), req)
		require.NoError(t, err)
		require.Equal(t, tc.want, res.Decision)
		require.Equal(t, []string{req.Prompt}, handler.prompts)
	}
}

func TestPromptApprovalHandler(t *testing.T) {
	req := &ApprovalRequest{Kind: "git-push", Prompt: "Allow push to x @ y?"}
	for _, tc := range []struct {
		decision, want ApprovalDecision
	}{
		{APPROVAL_DENY, APPROVAL_DENY},
		{APPROVAL_ALLOW_ONCE, APPROVAL_ALLOW_ONCE},
		{APPROVAL_ALLOW_SESSION, APPROVAL_ALLOW_SESSION},
		// Forever was not offered without a persistent key.
		{APPROVAL_ALLOW_FOREVER, APPROVAL_ALLOW_SESSION},
	} {
		handler := &approvalHandler{decision: tc.decision}
		res, err := NewPromptAttachable(handler).PromptApproval(t.Context(), req)
		require.NoError(t, err)
		require.Equal(t, tc.want, res.Decision)
		require.Empty(t, handler.prompts, "an ApprovalHandler is not asked yes/no")
	}
	_, err := NewPromptAttachable(nil).PromptApproval(t.Context(), req)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}
