package server

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Query.prompt's questions are remembered: for the session by the engine, and
// with a key by the owner's client, namespaced by the asking module.

func modulePrompt(message, key string) *core.PromptRequest {
	return &core.PromptRequest{Kind: "prompt", Message: message, Boolean: &core.PromptBoolean{}, Remember: true, Key: key}
}

func TestPromptRememberedForSession(t *testing.T) {
	var n atomic.Int32
	questions := &askPromptServer{answer: func(req *prompt.AskRequest) (*prompt.AskResponse, error) {
		n.Add(1)
		switch {
		case req.GetText() != nil:
			return &prompt.AskResponse{Answer: &prompt.AskResponse_Text{Text: "hi"}}, nil
		case req.GetSecret() != nil:
			return &prompt.AskResponse{Answer: &prompt.AskResponse_Secret{Secret: []byte("s3cr3t")}}, nil
		}
		return &prompt.AskResponse{Answer: &prompt.AskResponse_Boolean{Boolean: true}}, nil
	}}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	staff := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", testStaffPin)
	other := sessionTestModuleResultWithGitSource(t, "other", "https://github.com/org/other", testStaffPin)
	module := s.client("module", staff, &core.FunctionCall{ParentName: "Staff", Name: "ask"}, "owner")
	again := s.client("again", staff, &core.FunctionCall{ParentName: "Staff", Name: "other"}, "owner")
	otherModule := s.client("other", other, nil, "owner")

	for _, c := range []*clientRuntime{module, again} {
		answer, err := s.srv.Prompt(s.ctx(c), modulePrompt("Deploy?", ""))
		require.NoError(t, err)
		require.True(t, answer.Boolean)
	}
	require.EqualValues(t, 1, n.Load(), "the same module's same question is answered once a session, whichever function asks")

	for _, ask := range []struct {
		client *clientRuntime
		req    *core.PromptRequest
	}{
		{otherModule, modulePrompt("Deploy?", "")},                                                         // another module
		{module, modulePrompt("Deploy now?", "")},                                                          // another question
		{module, modulePrompt("Deploy?", "k")},                                                             // another key
		{module, &core.PromptRequest{Message: "Deploy?", Text: &core.PromptText{}, Remember: true}},        // another kind
		{s.owner, modulePrompt("Deploy?", "")},                                                             // no module
		{module, &core.PromptRequest{Message: "Deploy?", Secret: &core.PromptSecret{}, Remember: true}},    // a secret
		{module, &core.PromptRequest{Message: "Deploy?", Secret: &core.PromptSecret{}, Remember: true}},    // the same secret
		{module, &core.PromptRequest{Message: "Deploy?", Boolean: &core.PromptBoolean{}, Remember: false}}, // not remembered
	} {
		_, err := s.srv.Prompt(s.ctx(ask.client), ask.req)
		require.NoError(t, err)
	}
	require.EqualValues(t, 8, n.Load(), "each other question asks; a secret is remembered for the session too")

	// Another session asks again.
	s2 := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	module2 := s2.client("module", staff, nil, "owner")
	_, err := s2.srv.Prompt(s2.ctx(module2), modulePrompt("Deploy?", ""))
	require.NoError(t, err)
	require.EqualValues(t, 9, n.Load(), "answers do not outlive the session in the engine")
}

func TestPromptFailuresNotRemembered(t *testing.T) {
	var n atomic.Int32
	questions := &askPromptServer{answer: func(*prompt.AskRequest) (*prompt.AskResponse, error) {
		if n.Add(1) == 1 {
			return nil, status.Error(codes.FailedPrecondition, "not now")
		}
		return &prompt.AskResponse{Answer: &prompt.AskResponse_Boolean{Boolean: false}}, nil
	}}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	module := s.client("module", sessionTestModuleResult(t, "staff"), nil, "owner")
	_, err := s.srv.Prompt(s.ctx(module), modulePrompt("Deploy?", ""))
	require.ErrorIs(t, err, core.ErrPromptNotInteractive)
	answer, err := s.srv.Prompt(s.ctx(module), modulePrompt("Deploy?", ""))
	require.NoError(t, err)
	require.False(t, answer.Boolean)
	require.EqualValues(t, 2, n.Load(), "nobody answering is no answer: ask again")
}

func TestPromptRememberedConcurrently(t *testing.T) {
	release := make(chan struct{})
	var n atomic.Int32
	questions := &askPromptServer{answer: func(*prompt.AskRequest) (*prompt.AskResponse, error) {
		n.Add(1)
		<-release
		return &prompt.AskResponse{Answer: &prompt.AskResponse_Boolean{Boolean: true}}, nil
	}}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	module := s.client("module", sessionTestModuleResult(t, "staff"), nil, "owner")
	ctx := s.ctx(module)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			answer, err := s.srv.Prompt(ctx, modulePrompt("Deploy?", ""))
			if err != nil || !answer.Boolean {
				t.Errorf("prompt = %v, %v", answer, err)
			}
		})
	}
	require.Eventually(t, func() bool { return n.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
	close(release)
	wg.Wait()
	require.EqualValues(t, 1, n.Load(), "concurrent questions share the one prompt")
}

func TestPromptPersistentKey(t *testing.T) {
	var asked []*prompt.AskRequest
	var mu sync.Mutex
	questions := &askPromptServer{answer: func(req *prompt.AskRequest) (*prompt.AskResponse, error) {
		mu.Lock()
		asked = append(asked, req)
		mu.Unlock()
		if req.GetSecret() != nil {
			return &prompt.AskResponse{Answer: &prompt.AskResponse_Secret{Secret: []byte("x")}}, nil
		}
		return &prompt.AskResponse{Answer: &prompt.AskResponse_Boolean{Boolean: true}}, nil
	}}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	staff := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", testStaffPin)
	local := &core.Module{NameField: "staff", Source: dagql.NonNull(sessionTestModuleSource(t, &core.ModuleSource{
		Kind:              core.ModuleSourceKindLocal,
		Local:             &core.LocalModuleSource{ContextDirectoryPath: "/work/staff"},
		SourceRootSubpath: ".",
	}))}
	gitClient := s.client("git", staff, nil, "owner")
	localClient := s.client("local", sessionTestModuleOf(t, local), nil, "owner")

	for _, ask := range []struct {
		client *clientRuntime
		req    *core.PromptRequest
	}{
		{gitClient, modulePrompt("Deploy?", "deploy")},
		{gitClient, modulePrompt("Deploy?", "")},
		{localClient, modulePrompt("Deploy?", "deploy")},
		{s.owner, modulePrompt("Deploy?", "deploy")},
		{gitClient, &core.PromptRequest{Message: "Token?", Secret: &core.PromptSecret{}, Remember: true, Key: "deploy"}},
	} {
		_, err := s.srv.Prompt(s.ctx(ask.client), ask.req)
		require.NoError(t, err)
	}
	require.Len(t, asked, 5)
	require.Equal(t, `prompt:v1:"boolean":"https://github.com/org/staff|staff":"deploy"`, asked[0].PersistentKey,
		"a git module's key is namespaced by its source and the kind of answer")
	require.Empty(t, asked[1].PersistentKey, "nothing is remembered beyond the session without a key")
	require.Empty(t, asked[2].PersistentKey, "a local module is only a path: session memory only")
	require.Empty(t, asked[3].PersistentKey, "nor without a module")
	require.Empty(t, asked[4].PersistentKey, "a secret is never remembered beyond the session")

	repinned := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", "2222222222222222222222222222222222222222")
	impostor := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/evil/staff", testStaffPin)
	require.Equal(t, asked[0].PersistentKey, promptPersistentKey("boolean", repinned, "deploy"), "an update keeps its answers")
	require.NotEqual(t, asked[0].PersistentKey, promptPersistentKey("boolean", impostor, "deploy"), "another source cannot read them")
	require.NotEqual(t, asked[0].PersistentKey, promptPersistentKey("string", staff, "deploy"), "nor an answer of another kind")
	require.NotEqual(t,
		promptPersistentKey("boolean", staff, `deploy":"x`),
		promptPersistentKey("boolean", staff, `deploy`)+`:"x"`,
		"a key cannot spell another's separator")
}
