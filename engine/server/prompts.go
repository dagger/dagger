package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/session/prompt"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Prompt asks the user a question on behalf of the calling code: the prompt
// broker every engine-originated question (beside approvals, see
// requestApproval) goes through.
//
// As with approvals, the engine resolves both ends itself, never taking the
// caller's word for either: who is asked is the owner, the trusted client
// before the first module or inert boundary (see gitPushOwner), whose
// terminal the user answers on; who asks is the agent, module and function
// running the calling client (see resolveApprovalRequester), which the
// question names. The calling span anchors the question, so a frontend can
// show it beneath the call that asked rather than take over the screen.
//
// An owner whose client cannot ask (no terminal, or a client predating the
// kind of question) fails with an error wrapping core.ErrPromptNotInteractive,
// so the caller can fall back to a default.
func (srv *Server) Prompt(ctx context.Context, spec *core.PromptRequest) (*core.PromptAnswer, error) {
	req, err := askRequest(spec)
	if err != nil {
		return nil, err
	}
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	owner, _, err := gitPushOwner(client)
	if err != nil {
		return nil, err
	}
	requester, err := resolveApprovalRequester(ctx, client)
	if err != nil {
		return nil, err
	}
	req.Requester = requester.proto()
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		req.TraceId = sc.TraceID().String()
		req.SpanId = sc.SpanID().String()
	}
	resp, err := srv.promptAsk(ctx, owner, req, spec)
	if err != nil {
		return nil, err
	}
	return &core.PromptAnswer{
		Boolean: resp.GetBoolean(),
		Text:    resp.GetText(),
		Choice:  resp.GetChoice(),
		Secret:  resp.GetSecret(),
	}, nil
}

// askRequest is spec as sent to the owner's client. The text is chosen by the
// caller, possibly a module, so terminal control characters are escaped: the
// title is literal text, the message Markdown (which keeps its newlines).
func askRequest(spec *core.PromptRequest) (*prompt.AskRequest, error) {
	req := &prompt.AskRequest{
		Kind:    spec.Kind,
		Title:   terminalSafeText(spec.Title, false),
		Message: terminalSafeText(spec.Message, true),
	}
	inputs := 0
	if b := spec.Boolean; b != nil {
		inputs++
		req.Input = &prompt.AskRequest_Boolean{Boolean: &prompt.AskBoolean{Default: b.Default}}
	}
	if t := spec.Text; t != nil {
		inputs++
		req.Input = &prompt.AskRequest_Text{Text: &prompt.AskText{
			Default:   t.Default,
			Multiline: t.Multiline,
		}}
	}
	if s := spec.Select; s != nil {
		inputs++
		sel := &prompt.AskSelect{DefaultChoice: s.Default}
		for _, c := range s.Choices {
			sel.Choices = append(sel.Choices, &prompt.SelectChoice{
				Id:    c.ID,
				Label: terminalSafeText(c.Label, false),
			})
		}
		req.Input = &prompt.AskRequest_Select{Select: sel}
	}
	if s := spec.Secret; s != nil {
		inputs++
		req.Input = &prompt.AskRequest_Secret{Secret: &prompt.AskSecret{Multiline: s.Multiline}}
	}
	if inputs != 1 {
		return nil, fmt.Errorf("a prompt asks for exactly one kind of answer, not %d", inputs)
	}
	if err := prompt.ValidateAsk(req); err != nil {
		return nil, fmt.Errorf("invalid prompt: %s", status.Convert(err).Message())
	}
	return req, nil
}

// promptAsk asks owner's client. A client predating PromptAsk gets the
// closest question it knows (see promptAskLegacy).
func (srv *Server) promptAsk(ctx context.Context, owner *clientRuntime, req *prompt.AskRequest, spec *core.PromptRequest) (*prompt.AskResponse, error) {
	conn, available, err := srv.SpecificClientAttachableConn(ctx, owner.clientID, core.SpecificClientAttachableConnOpts{IfAvailable: true})
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, core.ErrPromptNotInteractive
	}
	client := prompt.NewPromptClient(conn)
	resp, err := client.PromptAsk(ctx, req)
	switch status.Code(err) {
	case codes.OK:
	case codes.Unimplemented:
		resp, err = promptAskLegacy(ctx, client, req, spec)
		if err != nil {
			return nil, err
		}
	case codes.FailedPrecondition:
		return nil, core.ErrPromptNotInteractive
	default:
		return nil, err
	}
	if err := prompt.ValidAskAnswer(req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// promptAskLegacy asks a client predating PromptAsk the closest question it
// knows, naming who asks in the text: a yes/no question or a line of text,
// and a selection (or, for a client predating selections too, the yes/no
// question spec allows in its place). A secret is never asked: those
// clients cannot mask what is typed.
func promptAskLegacy(ctx context.Context, client prompt.PromptClient, req *prompt.AskRequest, spec *core.PromptRequest) (*prompt.AskResponse, error) {
	text := prompt.AskPromptText(req)
	notInteractive := func(err error) error {
		switch status.Code(err) {
		case codes.Unimplemented, codes.FailedPrecondition:
			return core.ErrPromptNotInteractive
		}
		return err
	}
	switch input := req.GetInput().(type) {
	case *prompt.AskRequest_Boolean:
		resp, err := client.PromptBool(ctx, &prompt.BoolRequest{
			Title:   req.GetTitle(),
			Prompt:  text,
			Default: input.Boolean.GetDefault(),
		})
		if err != nil {
			return nil, notInteractive(err)
		}
		return &prompt.AskResponse{Answer: &prompt.AskResponse_Boolean{Boolean: resp.GetResponse()}}, nil
	case *prompt.AskRequest_Text:
		resp, err := client.PromptString(ctx, &prompt.StringRequest{
			Title:   req.GetTitle(),
			Prompt:  text,
			Default: input.Text.GetDefault(),
		})
		if err != nil {
			return nil, notInteractive(err)
		}
		return &prompt.AskResponse{Answer: &prompt.AskResponse_Text{Text: resp.GetResponse()}}, nil
	case *prompt.AskRequest_Select:
		resp, err := client.PromptSelect(ctx, &prompt.SelectRequest{
			Title:         req.GetTitle(),
			Prompt:        text,
			Choices:       input.Select.GetChoices(),
			DefaultChoice: input.Select.GetDefaultChoice(),
		})
		if err == nil {
			return &prompt.AskResponse{Answer: &prompt.AskResponse_Choice{Choice: resp.GetChoice()}}, nil
		}
		fallback := spec.Select.BoolFallback
		if status.Code(err) != codes.Unimplemented || fallback == nil {
			return nil, notInteractive(err)
		}
		yes, err := client.PromptBool(ctx, &prompt.BoolRequest{Title: req.GetTitle(), Prompt: text})
		if err != nil {
			return nil, notInteractive(err)
		}
		choice := fallback[1]
		if yes.GetResponse() {
			choice = fallback[0]
		}
		return &prompt.AskResponse{Answer: &prompt.AskResponse_Choice{Choice: choice}}, nil
	default:
		return nil, core.ErrPromptNotInteractive
	}
}

// terminalSafeText escapes what a terminal would interpret rather than show
// in caller-chosen text: control characters (but newlines and tabs, in
// multiline text) and bidirectional overrides, which could disguise what the
// user is asked. Everything else is kept, Markdown and non-ASCII alike.
func terminalSafeText(s string, multiline bool) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case multiline && (r == '\n' || r == '\t'):
			b.WriteRune(r)
		case r < 0x20, r >= 0x7f && r < 0xa0, isBidiControl(r):
			quoted := strconv.QuoteRuneToASCII(r)
			b.WriteString(quoted[1 : len(quoted)-1])
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isBidiControl(r rune) bool {
	switch {
	case r == 0x061c, r == 0x200e, r == 0x200f:
		return true
	case r >= 0x202a && r <= 0x202e:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// llmApproval asks the owner to let a module use the LLM API. A module that
// was allowed forever by a client predating approvals was remembered under
// "allow_llm:<module URL>"; that grant still holds.
func llmApproval(moduleURL string) approvalSpec {
	return approvalSpec{
		Kind:      approvalKindLLM,
		Subject:   literalText(moduleURL),
		Question:  "Allow LLM access for " + literalText(moduleURL),
		Action:    "Use the LLM API",
		LegacyKey: "allow_llm:" + moduleURL,
	}
}

const approvalKindLLM = "llm"

// AuthorizeLLM asks the owner to approve a remote module's use of the LLM
// API, which --allow-llm did not already allow (see core.LLM.allowed). It is
// an approval like any other (see requestApproval): allowed once, for the
// session, or forever, for the module asking, by the owner whose terminal
// it is.
func (srv *Server) AuthorizeLLM(ctx context.Context, moduleURL string) error {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return err
	}
	owner, _, err := gitPushOwner(client)
	if err != nil {
		return err
	}
	hint := fmt.Sprintf("pass --allow-llm=%s or --allow-llm=all to allow", moduleURL)
	allowed, err := srv.requestApproval(ctx, client, owner, llmApproval(moduleURL))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return fmt.Errorf("module %s requires approval to access the LLM: %w; %s", moduleURL, err, hint)
	}
	if !allowed {
		return fmt.Errorf("module %s was denied LLM access; %s", moduleURL, hint)
	}
	return nil
}
