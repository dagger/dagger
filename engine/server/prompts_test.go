package server

import (
	"context"
	"sync"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// askPromptServer is an owner's client that answers asks.
type askPromptServer struct {
	prompt.UnimplementedPromptServer
	answer func(*prompt.AskRequest) (*prompt.AskResponse, error)

	mu       sync.Mutex
	requests []*prompt.AskRequest
}

func (p *askPromptServer) PromptAsk(_ context.Context, req *prompt.AskRequest) (*prompt.AskResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return p.answer(req)
}

func (p *askPromptServer) asked() []*prompt.AskRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*prompt.AskRequest(nil), p.requests...)
}

// legacyPromptServer is an owner's client predating PromptAsk, and maybe
// PromptSelect.
type legacyPromptServer struct {
	prompt.UnimplementedPromptServer
	noSelect bool
	yes      bool
	text     string
	choice   string

	mu      sync.Mutex
	bools   []*prompt.BoolRequest
	strings []*prompt.StringRequest
	selects []*prompt.SelectRequest
}

func (p *legacyPromptServer) PromptBool(_ context.Context, req *prompt.BoolRequest) (*prompt.BoolResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bools = append(p.bools, req)
	return &prompt.BoolResponse{Response: p.yes}, nil
}

func (p *legacyPromptServer) PromptString(_ context.Context, req *prompt.StringRequest) (*prompt.StringResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.strings = append(p.strings, req)
	return &prompt.StringResponse{Response: p.text}, nil
}

func (p *legacyPromptServer) PromptSelect(_ context.Context, req *prompt.SelectRequest) (*prompt.SelectResponse, error) {
	if p.noSelect {
		return nil, status.Error(codes.Unimplemented, "old client")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.selects = append(p.selects, req)
	return &prompt.SelectResponse{Choice: p.choice}, nil
}

func testSelectPrompt() *core.PromptRequest {
	return &core.PromptRequest{
		Kind:    "test-select",
		Title:   "Which?",
		Message: "Pick **one**.",
		Select: &core.PromptSelect{
			Choices:      []core.PromptChoice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}, {ID: "c", Label: "C"}},
			Default:      "c",
			BoolFallback: &[2]string{"a", "c"},
		},
	}
}

func TestPromptBroker(t *testing.T) {
	questions := &askPromptServer{answer: func(*prompt.AskRequest) (*prompt.AskResponse, error) {
		return &prompt.AskResponse{Answer: &prompt.AskResponse_Text{Text: "hi"}}, nil
	}}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	staff := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", testStaffPin)
	s.client("module", staff, &core.FunctionCall{ParentName: "Staff", Name: "ask"}, "owner")
	nested := s.client("nested", dagql.ObjectResult[*core.Module]{}, nil, "owner", "module")

	traceID := trace.TraceID{1, 2, 3}
	spanID := trace.SpanID{4, 5, 6}
	ctx := trace.ContextWithSpanContext(s.ctx(nested), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID}))
	ctx = core.AgentToContext(ctx, testAgent(t, "agent-1", "worker"))
	answer, err := s.srv.Prompt(ctx, &core.PromptRequest{
		Kind:    "test",
		Title:   "Name\x1b[2J?",
		Message: "Say **hi**\x1b[31m\nplease\u202e",
		Text:    &core.PromptText{Default: "x", Multiline: true},
	})
	require.NoError(t, err)
	require.Equal(t, "hi", answer.Text)

	require.Len(t, questions.asked(), 1, "the owner is asked")
	req := questions.asked()[0]
	require.Equal(t, "test", req.Kind)
	require.Equal(t, `Name\x1b[2J?`, req.Title, "the title is literal text")
	require.Equal(t, "Say **hi**\\x1b[31m\nplease\\u202e", req.Message, "Markdown keeps its lines, not its escapes")
	require.Equal(t, &prompt.AskText{Default: "x", Multiline: true}, req.GetText())
	require.Equal(t, &prompt.ApprovalRequester{
		AgentName:   "worker",
		AgentHandle: "agent-1",
		Module:      "staff",
		ModuleRef:   "https://github.com/org/staff@" + testStaffPin,
		Function:    "Staff.ask",
	}, req.Requester, "the engine names who asks: the module driving a nested client")
	require.Equal(t, traceID.String(), req.TraceId)
	require.Equal(t, spanID.String(), req.SpanId)
	require.Empty(t, req.PersistentKey)
}

func TestPromptBrokerAnswers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		req    *core.PromptRequest
		resp   *prompt.AskResponse
		want   *core.PromptAnswer
		errMsg string
	}{
		{
			name: "boolean",
			req:  &core.PromptRequest{Message: "ok?", Boolean: &core.PromptBoolean{}},
			resp: &prompt.AskResponse{Answer: &prompt.AskResponse_Boolean{Boolean: true}},
			want: &core.PromptAnswer{Boolean: true},
		},
		{
			name: "select",
			req:  testSelectPrompt(),
			resp: &prompt.AskResponse{Answer: &prompt.AskResponse_Choice{Choice: "b"}},
			want: &core.PromptAnswer{Choice: "b"},
		},
		{
			name: "secret",
			req:  &core.PromptRequest{Message: "token?", Secret: &core.PromptSecret{}},
			resp: &prompt.AskResponse{Answer: &prompt.AskResponse_Secret{Secret: []byte("s3cr3t")}},
			want: &core.PromptAnswer{Secret: []byte("s3cr3t")},
		},
		{
			name:   "a choice not offered",
			req:    testSelectPrompt(),
			resp:   &prompt.AskResponse{Answer: &prompt.AskResponse_Choice{Choice: "z"}},
			errMsg: "does not match",
		},
		{
			name:   "an answer of another kind",
			req:    &core.PromptRequest{Message: "ok?", Boolean: &core.PromptBoolean{}},
			resp:   &prompt.AskResponse{Answer: &prompt.AskResponse_Text{Text: "yes"}},
			errMsg: "does not match",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			questions := &askPromptServer{answer: func(*prompt.AskRequest) (*prompt.AskResponse, error) { return tc.resp, nil }}
			s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
			answer, err := s.srv.Prompt(s.ctx(s.owner), tc.req)
			if tc.errMsg != "" {
				require.ErrorContains(t, err, tc.errMsg)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, answer)
		})
	}
}

func TestPromptBrokerInvalid(t *testing.T) {
	s := newApprovalTestSession(t, func(*grpc.Server) {})
	for _, req := range []*core.PromptRequest{
		{Message: "nothing asked"},
		{Message: "two things", Boolean: &core.PromptBoolean{}, Text: &core.PromptText{}},
		{Boolean: &core.PromptBoolean{}},
		{Message: "no default", Select: &core.PromptSelect{Choices: []core.PromptChoice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}}},
	} {
		_, err := s.srv.Prompt(s.ctx(s.owner), req)
		require.Error(t, err)
		require.NotErrorIs(t, err, core.ErrPromptNotInteractive)
	}
}

func TestPromptBrokerNotInteractive(t *testing.T) {
	// No prompt attachable at all: the CLI had no terminal.
	s := newApprovalTestSession(t, func(*grpc.Server) {})
	_, err := s.srv.Prompt(s.ctx(s.owner), &core.PromptRequest{Message: "ok?", Boolean: &core.PromptBoolean{}})
	require.ErrorIs(t, err, core.ErrPromptNotInteractive)

	// An attachable without a frontend.
	s = newApprovalTestSession(t, func(srv *grpc.Server) { prompt.NewPromptAttachable(nil).Register(srv) })
	_, err = s.srv.Prompt(s.ctx(s.owner), &core.PromptRequest{Message: "ok?", Boolean: &core.PromptBoolean{}})
	require.ErrorIs(t, err, core.ErrPromptNotInteractive)
}

func TestPromptBrokerLegacyClient(t *testing.T) {
	staff := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", testStaffPin)
	setup := func(t *testing.T, legacy *legacyPromptServer) (*approvalTestSession, context.Context) {
		s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, legacy) })
		module := s.client("module", staff, nil, "owner")
		return s, s.ctx(module)
	}
	t.Run("boolean", func(t *testing.T) {
		legacy := &legacyPromptServer{yes: true}
		s, ctx := setup(t, legacy)
		answer, err := s.srv.Prompt(ctx, &core.PromptRequest{Title: "Proceed?", Message: "Shall we?", Boolean: &core.PromptBoolean{Default: true}})
		require.NoError(t, err)
		require.True(t, answer.Boolean)
		require.Len(t, legacy.bools, 1)
		require.Equal(t, "Proceed?", legacy.bools[0].Title)
		require.Equal(t, "Shall we?\n\n"+`(requested by module "staff")`, legacy.bools[0].Prompt, "the text names who asks")
		require.True(t, legacy.bools[0].Default)
		require.Empty(t, legacy.bools[0].PersistentKey)
	})
	t.Run("text", func(t *testing.T) {
		legacy := &legacyPromptServer{text: "hello"}
		s, ctx := setup(t, legacy)
		answer, err := s.srv.Prompt(ctx, &core.PromptRequest{Message: "Name?", Text: &core.PromptText{Default: "x"}})
		require.NoError(t, err)
		require.Equal(t, "hello", answer.Text)
		require.Len(t, legacy.strings, 1)
		require.Equal(t, "x", legacy.strings[0].Default)
	})
	t.Run("select", func(t *testing.T) {
		legacy := &legacyPromptServer{choice: "b"}
		s, ctx := setup(t, legacy)
		answer, err := s.srv.Prompt(ctx, testSelectPrompt())
		require.NoError(t, err)
		require.Equal(t, "b", answer.Choice)
		require.Len(t, legacy.selects, 1)
		require.Equal(t, "c", legacy.selects[0].DefaultChoice)
	})
	t.Run("select as yes/no", func(t *testing.T) {
		for _, yes := range []bool{true, false} {
			legacy := &legacyPromptServer{noSelect: true, yes: yes}
			s, ctx := setup(t, legacy)
			answer, err := s.srv.Prompt(ctx, testSelectPrompt())
			require.NoError(t, err)
			if yes {
				require.Equal(t, "a", answer.Choice)
			} else {
				require.Equal(t, "c", answer.Choice)
			}
			require.Len(t, legacy.bools, 1)
		}
		// Without a yes/no form of the question, nobody can answer.
		legacy := &legacyPromptServer{noSelect: true, yes: true}
		s, ctx := setup(t, legacy)
		req := testSelectPrompt()
		req.Select.BoolFallback = nil
		_, err := s.srv.Prompt(ctx, req)
		require.ErrorIs(t, err, core.ErrPromptNotInteractive)
		require.Empty(t, legacy.bools)
	})
	t.Run("secret", func(t *testing.T) {
		legacy := &legacyPromptServer{text: "visible"}
		s, ctx := setup(t, legacy)
		_, err := s.srv.Prompt(ctx, &core.PromptRequest{Message: "Token?", Secret: &core.PromptSecret{}})
		require.ErrorIs(t, err, core.ErrPromptNotInteractive, "a client that cannot mask input is never asked for a secret")
		require.Empty(t, legacy.strings)
	})
}

func TestPromptBrokerCanceled(t *testing.T) {
	questions := &askPromptServer{answer: func(*prompt.AskRequest) (*prompt.AskResponse, error) {
		return nil, status.Error(codes.Canceled, "ask canceled")
	}}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	_, err := s.srv.Prompt(s.ctx(s.owner), &core.PromptRequest{Message: "ok?", Boolean: &core.PromptBoolean{}})
	require.Error(t, err)
	require.NotErrorIs(t, err, core.ErrPromptNotInteractive, "a canceled question is not a missing terminal")
}

func TestLLMApproval(t *testing.T) {
	questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return prompt.APPROVAL_DENY }}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	direct := sessionTestModuleResultWithGitSource(t, "direct", "https://github.com/org/direct", testStaffPin)
	module := s.client("module", direct, &core.FunctionCall{ParentName: "Direct", Name: "prompt"}, "owner")

	err := s.srv.AuthorizeLLM(s.ctx(module), "github.com/org/direct")
	require.ErrorContains(t, err, "module github.com/org/direct was denied LLM access; pass --allow-llm=github.com/org/direct or --allow-llm=all to allow")
	require.Len(t, questions.asked(), 1)
	req := questions.asked()[0]
	require.Equal(t, "llm", req.Kind)
	require.Equal(t, "Use the LLM API?", req.Question, "frontends rendering the choices show the bare action")
	require.Equal(t, `Allow LLM access for github.com/org/direct (requested by module "direct", function "Direct.prompt")?`, req.Prompt,
		"text-only frontends name the module's source, not only the name it chose")
	require.Equal(t, "github.com/org/direct", req.Subject)
	require.Equal(t, `approval:v2:"llm":"https://github.com/org/direct|direct":"github.com/org/direct"`, req.PersistentKey)
	require.Equal(t, []string{"allow_llm:github.com/org/direct"}, req.LegacyPersistentKeys,
		"a module allowed forever before approvals stays allowed")

	questions.decide = func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return prompt.APPROVAL_ALLOW_SESSION }
	for range 2 {
		require.NoError(t, s.srv.AuthorizeLLM(s.ctx(module), "github.com/org/direct"))
	}
	require.Len(t, questions.asked(), 2, "a session grant holds for the session")

	s = newApprovalTestSession(t, func(*grpc.Server) {})
	module = s.client("module", direct, nil, "owner")
	err = s.srv.AuthorizeLLM(s.ctx(module), "github.com/org/direct")
	require.ErrorIs(t, err, core.ErrPromptNotInteractive)
	require.ErrorContains(t, err, "--allow-llm=github.com/org/direct")
}

func TestLLMApprovalLegacyClient(t *testing.T) {
	// A client predating approvals remembers its yes forever under the key
	// it always did.
	questions := &boolPromptServer{answer: func(*prompt.BoolRequest) bool { return true }}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	direct := sessionTestModuleResultWithGitSource(t, "direct", "https://github.com/org/direct", testStaffPin)
	module := s.client("module", direct, nil, "owner")
	require.NoError(t, s.srv.AuthorizeLLM(s.ctx(module), "github.com/org/direct"))
	require.Len(t, questions.requests, 1)
	require.Equal(t, "allow_llm:github.com/org/direct", questions.requests[0].PersistentKey)
	require.Equal(t, `Allow LLM access for github.com/org/direct (requested by module "direct")?`, questions.requests[0].Prompt)
}

func TestTerminalSafeText(t *testing.T) {
	require.Equal(t, `a\x1b[31mb\u202ec\x00`, terminalSafeText("a\x1b[31mb\u202ec\x00", false))
	require.Equal(t, `a\nb\tc`, terminalSafeText("a\nb\tc", false))
	require.Equal(t, "a\nb\tc", terminalSafeText("a\nb\tc", true))
	require.Equal(t, "héllo 👋🏽 **md**", terminalSafeText("héllo 👋🏽 **md**", true), "printable text is kept as is")
	require.Equal(t, `\u0085`, terminalSafeText("\u0085", true), "C1 controls are escaped")
}
