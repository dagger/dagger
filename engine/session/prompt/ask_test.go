package prompt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// useTempConfig points the remembered approvals and answers at a temporary
// directory, so tests never touch the user's.
func useTempConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldRoot, oldConfirmations, oldAnswers := configRoot, promptConfirmationsFile, promptAnswersFile
	configRoot = dir
	promptConfirmationsFile = filepath.Join(dir, "prompt-confirmations.json")
	promptAnswersFile = filepath.Join(dir, "prompt-answers.json")
	t.Cleanup(func() {
		configRoot, promptConfirmationsFile, promptAnswersFile = oldRoot, oldConfirmations, oldAnswers
	})
	return dir
}

// textHandler is a frontend with only plain prompts and forms, like plain and
// dots: one-line text through HandlePrompt, the rest through forms.
type textHandler struct {
	answer  any
	typed   string
	prompts []string
	forms   int
	views   []string
}

func (h *textHandler) HandlePrompt(_ context.Context, _, prompt string, dest any) error {
	h.prompts = append(h.prompts, prompt)
	switch d := dest.(type) {
	case *bool:
		*d = h.answer.(bool)
	case *string:
		*d = h.answer.(string)
	}
	return nil
}

func (h *textHandler) HandleForm(_ context.Context, form *huh.Form) error {
	h.forms++
	form.Init()
	field := form.GetFocusedField()
	field.Focus()
	field.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(h.typed)})
	h.views = append(h.views, field.View())
	return nil
}

type askHandler struct {
	textHandler
	resp  *AskResponse
	err   error
	asked []*AskRequest
}

func (h *askHandler) HandleAsk(_ context.Context, req *AskRequest) (*AskResponse, error) {
	h.asked = append(h.asked, req)
	return h.resp, h.err
}

func boolAsk(key string) *AskRequest {
	return &AskRequest{
		Kind:          "test",
		Title:         "Proceed?",
		Message:       "Shall **we**?",
		Requester:     &ApprovalRequester{AgentName: "worker", Module: "keeper", Function: "Keeper.go"},
		PersistentKey: key,
		Input:         &AskRequest_Boolean{Boolean: &AskBoolean{Default: true}},
	}
}

func TestPromptAskFallback(t *testing.T) {
	useTempConfig(t)
	t.Run("boolean", func(t *testing.T) {
		h := &textHandler{answer: false}
		res, err := NewPromptAttachable(h).PromptAsk(t.Context(), boolAsk(""))
		require.NoError(t, err)
		require.Equal(t, &AskResponse{Answer: &AskResponse_Boolean{Boolean: false}}, res)
		// Plain frontends show only the prompt: it names who asks.
		require.Equal(t, []string{"Shall **we**?\n\n" + `(requested by agent "worker", module "keeper", function "Keeper.go")`}, h.prompts)
	})
	t.Run("text", func(t *testing.T) {
		h := &textHandler{answer: "hello"}
		req := &AskRequest{Title: "Name?", Input: &AskRequest_Text{Text: &AskText{}}}
		res, err := NewPromptAttachable(h).PromptAsk(t.Context(), req)
		require.NoError(t, err)
		require.Equal(t, "hello", res.GetText())
		require.Equal(t, []string{"Name?"}, h.prompts)
	})
	t.Run("multiline text uses a form", func(t *testing.T) {
		h := &textHandler{typed: "abc"}
		req := &AskRequest{Message: "Notes?", Input: &AskRequest_Text{Text: &AskText{Multiline: true}}}
		res, err := NewPromptAttachable(h).PromptAsk(t.Context(), req)
		require.NoError(t, err)
		require.Equal(t, "abc", res.GetText())
		require.Equal(t, 1, h.forms)
		require.Empty(t, h.prompts)
	})
	t.Run("secret is masked", func(t *testing.T) {
		h := &textHandler{typed: "hunter2"}
		req := &AskRequest{Message: "Token?", Input: &AskRequest_Secret{Secret: &AskSecret{}}}
		res, err := NewPromptAttachable(h).PromptAsk(t.Context(), req)
		require.NoError(t, err)
		require.Equal(t, []byte("hunter2"), res.GetSecret())
		require.Empty(t, h.prompts, "a secret must never be asked through a plain prompt")
		require.Len(t, h.views, 1)
		require.NotContains(t, h.views[0], "hunter2")
	})
	t.Run("select", func(t *testing.T) {
		h := &textHandler{}
		req := &AskRequest{Message: "Which?", Input: &AskRequest_Select{Select: &AskSelect{
			Choices:       []*SelectChoice{{Id: "a", Label: "A"}, {Id: "b", Label: "B"}},
			DefaultChoice: "b",
		}}}
		res, err := NewPromptAttachable(h).PromptAsk(t.Context(), req)
		require.NoError(t, err)
		require.Equal(t, "b", res.GetChoice())
	})
}

func TestPromptAskHandler(t *testing.T) {
	useTempConfig(t)
	h := &askHandler{resp: &AskResponse{Answer: &AskResponse_Boolean{Boolean: true}}}
	res, err := NewPromptAttachable(h).PromptAsk(t.Context(), boolAsk(""))
	require.NoError(t, err)
	require.True(t, res.GetBoolean())
	require.Len(t, h.asked, 1)
	require.Empty(t, h.prompts, "an AskHandler is not asked through plain prompts")

	// An answer of another kind is refused.
	h = &askHandler{resp: &AskResponse{Answer: &AskResponse_Text{Text: "yes"}}}
	_, err = NewPromptAttachable(h).PromptAsk(t.Context(), boolAsk(""))
	require.Equal(t, codes.Internal, status.Code(err))

	// A choice that was not offered is refused.
	h = &askHandler{resp: &AskResponse{Answer: &AskResponse_Choice{Choice: "c"}}}
	_, err = NewPromptAttachable(h).PromptAsk(t.Context(), &AskRequest{Message: "Which?", Input: &AskRequest_Select{Select: &AskSelect{
		Choices:       []*SelectChoice{{Id: "a", Label: "A"}, {Id: "b", Label: "B"}},
		DefaultChoice: "a",
	}}})
	require.Equal(t, codes.Internal, status.Code(err))

	// A canceled ask is canceled, not failed.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	h = &askHandler{err: context.Canceled}
	_, err = NewPromptAttachable(h).PromptAsk(ctx, boolAsk(""))
	require.Equal(t, codes.Canceled, status.Code(err))

	h = &askHandler{err: errors.New("boom")}
	_, err = NewPromptAttachable(h).PromptAsk(t.Context(), boolAsk(""))
	require.Equal(t, codes.Internal, status.Code(err))

	// Without a frontend nobody can answer.
	_, err = NewPromptAttachable(nil).PromptAsk(t.Context(), boolAsk(""))
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestPromptAskInvalid(t *testing.T) {
	for _, req := range []*AskRequest{
		{Input: &AskRequest_Boolean{Boolean: &AskBoolean{}}},
		{Message: "no input"},
		{Message: "one choice", Input: &AskRequest_Select{Select: &AskSelect{
			Choices: []*SelectChoice{{Id: "a", Label: "A"}}, DefaultChoice: "a",
		}}},
		{Message: "no default", Input: &AskRequest_Select{Select: &AskSelect{
			Choices: []*SelectChoice{{Id: "a", Label: "A"}, {Id: "b", Label: "B"}},
		}}},
	} {
		_, err := NewPromptAttachable(&askHandler{}).PromptAsk(t.Context(), req)
		require.Equal(t, codes.InvalidArgument, status.Code(err), "%v", req)
	}
}

func TestPromptAskRemembers(t *testing.T) {
	dir := useTempConfig(t)
	h := &askHandler{resp: &AskResponse{Answer: &AskResponse_Boolean{Boolean: false}}}
	p := NewPromptAttachable(h)
	for range 2 {
		res, err := p.PromptAsk(t.Context(), boolAsk("k1"))
		require.NoError(t, err)
		require.False(t, res.GetBoolean())
	}
	require.Len(t, h.asked, 1, "a remembered answer answers without asking")

	// A remembered answer of another kind does not answer.
	h.resp = &AskResponse{Answer: &AskResponse_Text{Text: "x"}}
	req := &AskRequest{Message: "Text?", PersistentKey: "k1", Input: &AskRequest_Text{Text: &AskText{}}}
	res, err := p.PromptAsk(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "x", res.GetText())
	require.Len(t, h.asked, 2)

	// A secret is never remembered, whatever the request says.
	h.resp = &AskResponse{Answer: &AskResponse_Secret{Secret: []byte("hunter2")}}
	secret := &AskRequest{Message: "Token?", PersistentKey: "k2", Input: &AskRequest_Secret{Secret: &AskSecret{}}}
	for range 2 {
		_, err := p.PromptAsk(t.Context(), secret)
		require.NoError(t, err)
	}
	require.Len(t, h.asked, 4)
	data, err := os.ReadFile(filepath.Join(dir, "prompt-answers.json"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "k2")
	require.NotContains(t, string(data), "hunter2")
	info, err := os.Stat(filepath.Join(dir, "prompt-answers.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestPromptApprovalLegacyKeys(t *testing.T) {
	useTempConfig(t)
	var p PromptResponses
	promptMutex.Lock()
	require.NoError(t, p.persistResponse("allow_llm:github.com/a/b"))
	promptMutex.Unlock()

	h := &approvalHandler{decision: APPROVAL_DENY}
	req := &ApprovalRequest{
		Kind: "llm", Prompt: "Allow LLM access?", PersistentKey: "approval:v2:llm",
		LegacyPersistentKeys: []string{"allow_llm:github.com/a/b"},
	}
	res, err := NewPromptAttachable(h).PromptApproval(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, APPROVAL_ALLOW_FOREVER, res.Decision, "a grant an older client remembered still holds")

	// Without a key of today's, no forever grant is offered, so none answers.
	req.PersistentKey = ""
	res, err = NewPromptAttachable(h).PromptApproval(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, APPROVAL_DENY, res.Decision)

	// A new forever grant goes under today's key only.
	req = &ApprovalRequest{
		Kind: "llm", Prompt: "Allow LLM access?", PersistentKey: "approval:v2:llm:new",
		LegacyPersistentKeys: []string{"allow_llm:github.com/c/d"},
	}
	h.decision = APPROVAL_ALLOW_FOREVER
	_, err = NewPromptAttachable(h).PromptApproval(t.Context(), req)
	require.NoError(t, err)
	data, err := os.ReadFile(promptConfirmationsFile)
	require.NoError(t, err)
	require.Contains(t, string(data), "approval:v2:llm:new")
	require.False(t, strings.Contains(string(data), "allow_llm:github.com/c/d"))
}
