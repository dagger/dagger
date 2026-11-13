package server

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// approvalPromptServer is an owner's client that answers approvals.
type approvalPromptServer struct {
	prompt.UnimplementedPromptServer
	decide func(*prompt.ApprovalRequest) prompt.ApprovalDecision

	mu       sync.Mutex
	requests []*prompt.ApprovalRequest
}

func (p *approvalPromptServer) PromptApproval(_ context.Context, req *prompt.ApprovalRequest) (*prompt.ApprovalResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return &prompt.ApprovalResponse{Decision: p.decide(req)}, nil
}

func (p *approvalPromptServer) asked() []*prompt.ApprovalRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*prompt.ApprovalRequest(nil), p.requests...)
}

func (p *approvalPromptServer) prompts() []string {
	var prompts []string
	for _, req := range p.asked() {
		prompts = append(prompts, req.Prompt)
	}
	return prompts
}

// boolPromptServer is an owner's client predating PromptApproval.
type boolPromptServer struct {
	prompt.UnimplementedPromptServer
	answer func(*prompt.BoolRequest) bool

	mu       sync.Mutex
	requests []*prompt.BoolRequest
}

func (p *boolPromptServer) PromptBool(_ context.Context, req *prompt.BoolRequest) (*prompt.BoolResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return &prompt.BoolResponse{Response: p.answer(req)}, nil
}

// promptTestConn serves the owner's attachables, as register installs them,
// to the engine over an in-memory connection.
func promptTestConn(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	register(grpcServer)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)
	conn, err := grpc.NewClient("passthrough:///prompt", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	return conn
}

// approvalTestSession is a session whose "owner" client answers prompts with
// the attachables register installs.
type approvalTestSession struct {
	t     *testing.T
	srv   *Server
	sess  *daggerSession
	owner *clientRuntime
}

func newApprovalTestSession(t *testing.T, register func(*grpc.Server)) *approvalTestSession {
	t.Helper()
	conn := promptTestConn(t, register)
	sess := &daggerSession{sessionID: "session", attachables: newSessionAttachableManager()}
	sess.state.Store(sessionStateInitialized)
	sess.clientRuntimes = map[string]*clientRuntime{}
	s := &approvalTestSession{t: t, srv: &Server{daggerSessions: map[string]*daggerSession{"session": sess}}, sess: sess}
	s.owner = s.client("owner", dagql.ObjectResult[*core.Module]{}, nil)
	sess.attachables.callers["owner"] = &sessionAttachableCaller{ctx: t.Context(), conn: conn}
	return s
}

// client adds a client running mod's function fnCall (if any), called by
// parents.
func (s *approvalTestSession) client(id string, mod dagql.ObjectResult[*core.Module], fnCall *core.FunctionCall, parents ...string) *clientRuntime {
	client := &clientRuntime{clientRecord: &clientRecord{
		clientID: id, daggerSession: s.sess,
		clientMetadata:  &engine.ClientMetadata{ClientID: id, SessionID: "session"},
		parentClientIDs: parents, metadataSealed: true, accepting: true,
	}, state: clientStateInitialized, lifecycleLeases: make(map[uint64]clientLifecycleLeaseRecord),
		mod: mod, fnCall: fnCall}
	s.sess.clientRuntimes[id] = client
	installTestClientRecords(s.sess)
	return client
}

func (s *approvalTestSession) ctx(client *clientRuntime) context.Context {
	scope, err := s.sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "approval test")
	require.NoError(s.t, err)
	s.t.Cleanup(scope.Lease().Release)
	ctx, err := engine.ContextWithClientScope(s.t.Context(), scope)
	require.NoError(s.t, err)
	return ctx
}

func (s *approvalTestSession) request(ctx context.Context, client *clientRuntime, spec approvalSpec) (bool, error) {
	return s.srv.requestApproval(ctx, client, s.owner, spec)
}

func testAgent(t *testing.T, handle, name string) dagql.ObjectResult[*core.Agent] {
	t.Helper()
	dag, err := dagql.NewServer(t.Context(), &core.Agent{})
	require.NoError(t, err)
	res, err := dagql.NewObjectResultForCall(&core.Agent{Handle: handle, Name: name}, dag,
		&dagql.ResultCall{SyntheticOp: "approval-test-agent-" + handle})
	require.NoError(t, err)
	return res
}

const testStaffPin = "1111111111111111111111111111111111111111"

func testReadSpec(subject string) approvalSpec {
	return approvalSpec{Kind: "test-read", Subject: subject, Question: "Allow reading " + subject}
}

func TestApprovalRequest(t *testing.T) {
	questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return prompt.APPROVAL_ALLOW_ONCE }}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	staff := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", testStaffPin)
	s.client("module", staff, &core.FunctionCall{ParentName: "Staff", Name: "gh"}, "owner")
	nested := s.client("nested", dagql.ObjectResult[*core.Module]{}, nil, "owner", "module")

	traceID := trace.TraceID{1, 2, 3}
	spanID := trace.SpanID{4, 5, 6}
	ctx := trace.ContextWithSpanContext(s.ctx(nested), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID}))
	ctx = core.AgentToContext(ctx, testAgent(t, "agent-1", "worker"))
	allowed, err := s.request(ctx, nested, approvalSpec{Kind: "test-push", Subject: "remote @ ref", Question: "Allow push to remote @ ref", Danger: true})
	require.NoError(t, err)
	require.True(t, allowed)

	require.Len(t, questions.asked(), 1)
	req := questions.asked()[0]
	require.Equal(t, "test-push", req.Kind)
	require.Equal(t, "remote @ ref", req.Subject)
	require.True(t, req.Danger)
	require.Equal(t, `Allow push to remote @ ref (requested by agent "worker", module "staff", function "Staff.gh")?`, req.Prompt,
		"the module driving a nested client asks, running its function")
	require.Equal(t, &prompt.ApprovalRequester{
		AgentName:   "worker",
		AgentHandle: "agent-1",
		Module:      "staff",
		ModuleRef:   "https://github.com/org/staff@" + testStaffPin,
		Function:    "Staff.gh",
	}, req.Requester)
	require.Equal(t, traceID.String(), req.TraceId)
	require.Equal(t, spanID.String(), req.SpanId)
	require.Empty(t, req.PersistentKey, "a dangerous operation is never offered forever")
}

func TestApprovalDangerNeverForever(t *testing.T) {
	questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return prompt.APPROVAL_ALLOW_FOREVER }}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	module := s.client("module", sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", testStaffPin), nil, "owner")
	ctx := s.ctx(module)
	force := approvalSpec{Kind: "test-force-push", Subject: "remote @ ref", Question: "Allow force-push to remote @ ref", Danger: true}
	for range 2 {
		allowed, err := s.request(ctx, module, force)
		require.NoError(t, err)
		require.True(t, allowed)
	}
	_, err := s.request(ctx, module, approvalSpec{Kind: "test-push", Subject: "remote @ ref", Question: "Allow push to remote @ ref"})
	require.NoError(t, err)
	asked := questions.asked()
	require.Len(t, asked, 2, "a forever answer to an unoffered forever lasts the session")
	require.Empty(t, asked[0].PersistentKey, "a force-push is never offered forever")
	require.Equal(t, `approval:v2:"test-push":"https://github.com/org/staff|staff":"remote @ ref"`, asked[1].PersistentKey)
}

func TestApprovalRequesterEscaped(t *testing.T) {
	r := approvalRequester{
		agentName:   "worker\x1b[31m",
		agentHandle: "agent-1",
		module:      sessionTestModuleResultWithGitSource(t, "staff\u202e", "https://github.com/org/st\u202eaff", testStaffPin),
		function:    "Staff.gh\x00",
	}
	require.Equal(t, &prompt.ApprovalRequester{
		AgentName:   `worker\x1b[31m`,
		AgentHandle: "agent-1",
		Module:      `staff\u202e`,
		ModuleRef:   `https://github.com/org/st\u202eaff@` + testStaffPin,
		Function:    `Staff.gh\x00`,
	}, r.proto(), "names chosen by modules and spawners arrive as literal text")
}

func TestApprovalAgentFromFunctionCall(t *testing.T) {
	// A module's container calls back through a nested client: no agent loop
	// context there, only the agent stamped on the module's function call.
	questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return prompt.APPROVAL_ALLOW_SESSION }}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	staff := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", testStaffPin)
	stamped := func(agent dagql.ObjectResult[*core.Agent]) *core.FunctionCall {
		fnCall := &core.FunctionCall{ParentName: "Staff", Name: "gh"}
		fnCall.SetCallerAgent(agent)
		return fnCall
	}
	s.client("chief", staff, stamped(testAgent(t, "agent-1", "chief")), "owner")
	chief := s.client("chief-nested", dagql.ObjectResult[*core.Module]{}, nil, "owner", "chief")
	s.client("worker", staff, stamped(testAgent(t, "agent-2", "worker")), "owner")
	worker := s.client("worker-nested", dagql.ObjectResult[*core.Module]{}, nil, "owner", "worker")
	s.client("other", sessionTestModuleResultWithGitSource(t, "other", "https://github.com/org/other", testStaffPin),
		stamped(testAgent(t, "agent-1", "chief")), "owner")
	other := s.client("other-nested", dagql.ObjectResult[*core.Module]{}, nil, "owner", "other")

	allowed, err := s.request(s.ctx(chief), chief, testReadSpec("repo"))
	require.NoError(t, err)
	require.True(t, allowed)
	req := questions.asked()[0]
	require.Equal(t, "chief", req.Requester.AgentName, "the prompt names the agent")
	require.Equal(t, "agent-1", req.Requester.AgentHandle)
	require.Contains(t, req.Prompt, `agent "chief"`)

	// The grant is the module's: another agent using it shares it.
	allowed, err = s.request(s.ctx(worker), worker, testReadSpec("repo"))
	require.NoError(t, err)
	require.True(t, allowed)
	require.Len(t, questions.asked(), 1, "agents of one module share its grant")

	// Another module is asked about separately, even for the same agent.
	_, err = s.request(s.ctx(other), other, testReadSpec("repo"))
	require.NoError(t, err)
	require.Len(t, questions.asked(), 2, "modules do not share grants")
}

func TestApprovalGrantScope(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decision prompt.ApprovalDecision
		// prompts is how many times two identical requests ask
		prompts int
		allowed bool
	}{
		{"deny is not remembered", prompt.APPROVAL_DENY, 2, false},
		{"once is not remembered", prompt.APPROVAL_ALLOW_ONCE, 2, true},
		{"session is remembered", prompt.APPROVAL_ALLOW_SESSION, 1, true},
		{"forever is remembered for the session", prompt.APPROVAL_ALLOW_FOREVER, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return tc.decision }}
			s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
			staff := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", testStaffPin)
			module := s.client("module", staff, nil, "owner")
			ctx := core.AgentToContext(s.ctx(module), testAgent(t, "agent-1", "worker"))
			for range 2 {
				allowed, err := s.request(ctx, module, testReadSpec("repo"))
				require.NoError(t, err)
				require.Equal(t, tc.allowed, allowed)
			}
			require.Len(t, questions.asked(), tc.prompts)
			require.NotEmpty(t, questions.asked()[0].PersistentKey)

			// Another subject or kind is another grant.
			_, err := s.request(ctx, module, testReadSpec("other"))
			require.NoError(t, err)
			_, err = s.request(ctx, module, approvalSpec{Kind: "test-write", Subject: "repo", Question: "Allow writing repo"})
			require.NoError(t, err)
			require.Len(t, questions.asked(), tc.prompts+2)
		})
	}
}

func TestApprovalGrantRequester(t *testing.T) {
	questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return prompt.APPROVAL_ALLOW_SESSION }}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	staff := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", testStaffPin)
	spawn := s.client("spawn", staff, &core.FunctionCall{ParentName: "Staff", Name: "spawn"}, "owner")
	gh := s.client("gh", staff, &core.FunctionCall{ParentName: "Staff", Name: "gh"}, "owner")
	other := s.client("other", sessionTestModuleResultWithGitSource(t, "other", "https://github.com/org/other", testStaffPin), nil, "owner")
	impostor := s.client("impostor", sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/evil/staff", testStaffPin), nil, "owner")
	worker := testAgent(t, "agent-1", "worker")
	respawned := testAgent(t, "agent-2", "worker")
	spec := testReadSpec("repo")

	allowed, err := s.request(core.AgentToContext(s.ctx(spawn), worker), spawn, spec)
	require.NoError(t, err)
	require.True(t, allowed)
	// The grant is the module's, whichever function or agent asks.
	for _, req := range []struct {
		ctx    context.Context
		client *clientRuntime
	}{
		{core.AgentToContext(s.ctx(gh), worker), gh},
		{core.AgentToContext(s.ctx(gh), respawned), gh},
		{s.ctx(gh), gh},
	} {
		allowed, err := s.request(req.ctx, req.client, spec)
		require.NoError(t, err)
		require.True(t, allowed)
	}
	require.Len(t, questions.asked(), 1, "a grant holds across the module's functions and agents")

	// Another module, or a module only named alike, is asked separately.
	for _, req := range []struct {
		ctx    context.Context
		client *clientRuntime
	}{
		{core.AgentToContext(s.ctx(other), worker), other},
		{core.AgentToContext(s.ctx(impostor), worker), impostor},
	} {
		allowed, err := s.request(req.ctx, req.client, spec)
		require.NoError(t, err)
		require.True(t, allowed)
	}
	asked := questions.asked()
	require.Len(t, asked, 3)
	for _, req := range asked[1:] {
		require.NotEqual(t, asked[0].PersistentKey, req.PersistentKey, "nor do they share forever grants")
	}
}

func TestApprovalPersistentKey(t *testing.T) {
	staff := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", testStaffPin)
	repinned := sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", "2222222222222222222222222222222222222222")
	key := approvalPersistentKey("k", staff, "s")
	require.Equal(t, `approval:v2:"k":"https://github.com/org/staff|staff":"s"`, key)
	require.Equal(t, key, approvalPersistentKey("k", repinned, "s"), "an update of the module keeps its forever grant")
	// Quoting keeps each field's separators its own.
	require.NotEqual(t,
		approvalPersistentKey(`k":"https://github.com/org/staff|staff`, staff, "s"),
		approvalPersistentKey("k", staff, `https://github.com/org/staff|staff":"s`))
	require.Empty(t, approvalPersistentKey("k", dagql.ObjectResult[*core.Module]{}, "s"), "nothing to remember without a module")
	require.Empty(t, approvalPersistentKey("k", sessionTestModuleResult(t, "nosource"), "s"),
		"a module without a source is not identifiable")
	dir := &core.Module{NameField: "staff", Source: dagql.NonNull(sessionTestModuleSource(t, &core.ModuleSource{Kind: core.ModuleSourceKindDir}))}
	require.Empty(t, approvalPersistentKey("k", sessionTestModuleOf(t, dir), "s"),
		"a module built from a directory has no source the engine can name")
	local := &core.Module{NameField: "staff", Source: dagql.NonNull(sessionTestModuleSource(t, &core.ModuleSource{
		Kind:              core.ModuleSourceKindLocal,
		Local:             &core.LocalModuleSource{ContextDirectoryPath: "/work/staff"},
		SourceRootSubpath: ".",
	}))}
	require.Equal(t, "/work/staff", canonicalModuleReference(local.Source.Value.Self()))
	require.Empty(t, approvalPersistentKey("k", sessionTestModuleOf(t, local), "s"),
		"a local module is only a path, which another module can be loaded from")
}

func sessionTestModuleSource(t *testing.T, src *core.ModuleSource) dagql.ObjectResult[*core.ModuleSource] {
	t.Helper()
	dag, err := dagql.NewServer(t.Context(), &core.Module{})
	require.NoError(t, err)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.ModuleSource]{Typed: &core.ModuleSource{}}))
	res, err := dagql.NewObjectResultForCall(src, dag, &dagql.ResultCall{SyntheticOp: "approval-test-module-source"})
	require.NoError(t, err)
	return res
}

func sessionTestModuleOf(t *testing.T, mod *core.Module) dagql.ObjectResult[*core.Module] {
	t.Helper()
	dag, err := dagql.NewServer(t.Context(), &core.Module{})
	require.NoError(t, err)
	res, err := dagql.NewObjectResultForCall(mod, dag, &dagql.ResultCall{SyntheticOp: "approval-test-module-" + mod.Name()})
	require.NoError(t, err)
	return res
}

func TestApprovalForeverNotOffered(t *testing.T) {
	questions := &approvalPromptServer{decide: func(*prompt.ApprovalRequest) prompt.ApprovalDecision { return prompt.APPROVAL_ALLOW_FOREVER }}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	module := s.client("module", sessionTestModuleResult(t, "nosource"), nil, "owner")
	ctx := s.ctx(module)
	for range 2 {
		allowed, err := s.request(ctx, module, testReadSpec("repo"))
		require.NoError(t, err)
		require.True(t, allowed)
	}
	require.Len(t, questions.asked(), 1, "forever without a key is a session grant")
	require.Empty(t, questions.asked()[0].PersistentKey)
}

func TestApprovalFallsBackToBoolPrompt(t *testing.T) {
	questions := &boolPromptServer{answer: func(req *prompt.BoolRequest) bool { return req.Prompt == `Allow reading yes (requested by module "staff")?` }}
	s := newApprovalTestSession(t, func(srv *grpc.Server) { prompt.RegisterPromptServer(srv, questions) })
	module := s.client("module", sessionTestModuleResultWithGitSource(t, "staff", "https://github.com/org/staff", testStaffPin), nil, "owner")
	ctx := s.ctx(module)
	for range 2 {
		allowed, err := s.request(ctx, module, testReadSpec("yes"))
		require.NoError(t, err)
		require.True(t, allowed)
	}
	for range 2 {
		allowed, err := s.request(ctx, module, testReadSpec("no"))
		require.NoError(t, err)
		require.False(t, allowed)
	}
	require.Len(t, questions.requests, 3, "a yes allows for the session, a no only once")
	for _, req := range questions.requests {
		require.Empty(t, req.PersistentKey, "a yes must not be remembered forever")
	}
}

func TestApprovalNotInteractive(t *testing.T) {
	s := newApprovalTestSession(t, func(*grpc.Server) {})
	module := s.client("module", sessionTestModuleResult(t, "staff"), nil, "owner")
	_, err := s.request(s.ctx(module), module, testReadSpec("repo"))
	require.ErrorIs(t, err, errApprovalNotInteractive)
}

func TestApprovalGrantsCancellation(t *testing.T) {
	var grants approvalGrants
	key := approvalKey{kind: "k", subject: "s"}
	_, err := grants.check(t.Context(), key, func(context.Context) (prompt.ApprovalDecision, error) {
		return prompt.APPROVAL_ALLOW_SESSION, context.Canceled
	})
	require.ErrorIs(t, err, context.Canceled)
	calls := 0
	decision, err := grants.check(t.Context(), key, func(context.Context) (prompt.ApprovalDecision, error) {
		calls++
		return prompt.APPROVAL_ALLOW_SESSION, nil
	})
	require.NoError(t, err)
	require.Equal(t, prompt.APPROVAL_ALLOW_SESSION, decision)
	require.Equal(t, 1, calls, "a canceled prompt is no decision")
	var nextSession approvalGrants
	_, err = nextSession.check(t.Context(), key, func(context.Context) (prompt.ApprovalDecision, error) {
		calls++
		return prompt.APPROVAL_ALLOW_SESSION, nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, calls, "grants must not survive a session")
}

func TestApprovalGrantsConcurrent(t *testing.T) {
	for _, decision := range []prompt.ApprovalDecision{prompt.APPROVAL_ALLOW_ONCE, prompt.APPROVAL_ALLOW_SESSION} {
		t.Run(decision.String(), func(t *testing.T) {
			var grants approvalGrants
			key := approvalKey{kind: "k", subject: "s"}
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			ask := func(ctx context.Context) (prompt.ApprovalDecision, error) {
				if calls.Add(1) == 1 {
					close(started)
				}
				select {
				case <-release:
					return decision, nil
				case <-ctx.Done():
					return prompt.APPROVAL_DENY, ctx.Err()
				}
			}
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					if got, err := grants.check(t.Context(), key, ask); err != nil || got != decision {
						t.Errorf("approval = %v, %v", got, err)
					}
				})
			}
			<-started
			// A waiter giving up does not cancel the prompt for the others.
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			_, err := grants.check(canceled, key, ask)
			require.True(t, errors.Is(err, context.Canceled))
			close(release)
			wg.Wait()
			before := calls.Load()
			_, err = grants.check(t.Context(), key, ask)
			require.NoError(t, err)
			if decision == prompt.APPROVAL_ALLOW_ONCE {
				// Requests made while the prompt was open share its answer,
				// but one made after it returned asks again.
				require.Equal(t, before+1, calls.Load(), "allow once is not remembered")
				return
			}
			require.EqualValues(t, 1, calls.Load(), "requests share the one prompt and its session grant")
		})
	}
}
