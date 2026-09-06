package prompt

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type selectHandler struct {
	handle func(context.Context, *huh.Form) error
}

func (s selectHandler) HandlePrompt(context.Context, string, string, any) error {
	panic("not a bool/string prompt")
}
func (s selectHandler) HandleForm(ctx context.Context, f *huh.Form) error { return s.handle(ctx, f) }

func selectRequest() *SelectRequest {
	return &SelectRequest{Title: "Checkpoint?", Choices: []*SelectChoice{
		{Id: "include", Label: "Include"}, {Id: "drop", Label: "Drop"}, {Id: "cancel", Label: "Cancel"},
	}, DefaultChoice: "cancel"}
}

func TestPromptSelect(t *testing.T) {
	for _, choice := range []struct {
		name string
		up   int
	}{{"cancel", 0}, {"drop", 1}, {"include", 2}} {
		t.Run(choice.name, func(t *testing.T) {
			handler := selectHandler{func(_ context.Context, form *huh.Form) error {
				field := form.GetFocusedField()
				require.Equal(t, "cancel", field.GetValue())
				field.Focus()
				for range choice.up {
					field.Update(tea.KeyMsg{Type: tea.KeyUp})
				}
				return nil
			}}
			res, err := NewPromptAttachable(handler).PromptSelect(t.Context(), selectRequest())
			require.NoError(t, err)
			require.Equal(t, choice.name, res.Choice)
		})
	}
}

func TestPromptSelectInvalidAndUnavailable(t *testing.T) {
	for _, mutate := range []func(*SelectRequest){
		func(r *SelectRequest) { r.DefaultChoice = "" },
		func(r *SelectRequest) { r.Choices[1].Id = "include" },
		func(r *SelectRequest) { r.Choices[0] = nil },
		func(r *SelectRequest) { r.Choices = nil },
	} {
		r := selectRequest()
		mutate(r)
		_, err := NewPromptAttachable(nil).PromptSelect(t.Context(), r)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	}
	_, err := NewPromptAttachable(nil).PromptSelect(t.Context(), selectRequest())
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = NewPromptAttachable(nil).PromptSelect(ctx, selectRequest())
	require.ErrorIs(t, err, context.Canceled)
	_, err = NewPromptAttachable(selectHandler{func(_ context.Context, f *huh.Form) error { f.State = huh.StateAborted; return nil }}).PromptSelect(t.Context(), selectRequest())
	require.Equal(t, codes.Canceled, status.Code(err))
}
