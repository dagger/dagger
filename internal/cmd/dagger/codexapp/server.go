package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/dagger/dagger/engine/slog"
)

// ErrInterrupted is what a Conversation's Prompt returns when Interrupt
// preempted the turn. The turn then completes with the interrupted status
// rather than failed.
var ErrInterrupted = errors.New("turn interrupted")

// Backend is the Dagger side of the server: it mints conversations and
// answers the questions that need an engine.
type Backend interface {
	// Info describes the server for the initialize handshake and thread
	// metadata.
	Info() ServerInfo
	// NewThread composes a fresh conversation.
	NewThread(ctx context.Context, opts ThreadOptions) (Conversation, error)
	// LoadThread restores a saved conversation by thread id.
	LoadThread(ctx context.Context, threadID string) (Conversation, error)
	// SavedThreads lists the conversations saved on disk, newest first.
	SavedThreads(ctx context.Context) ([]SavedThread, error)
	// SavedThreadHistory renders a saved conversation's turns without
	// loading it.
	SavedThreadHistory(ctx context.Context, threadID string) ([]Turn, error)
	// Models lists the models the configured providers offer.
	Models(ctx context.Context) ([]Model, error)
}

// ServerInfo is static information about the server.
type ServerInfo struct {
	// Version is the Dagger CLI version.
	Version string
	// Cwd is the host directory the workspace is rooted at.
	Cwd string
	// Home is the directory Dagger keeps its configuration and sessions
	// under, reported as the protocol's codexHome.
	Home string
}

// ThreadOptions configure a new conversation.
type ThreadOptions struct {
	// ID is the thread id, pre-minted so it can double as the save identity.
	ID string
	// Model and Provider override the composed LLM's model when set.
	Model    string
	Provider string
	// Instructions is an extra system prompt.
	Instructions string
	// Ephemeral conversations are never saved.
	Ephemeral bool
}

// SavedThread is a conversation saved on disk.
type SavedThread struct {
	ID        string
	Name      string
	Model     string
	CreatedAt time.Time
}

// Conversation is one thread's engine-side state: the composed LLM and the
// agent runtime driving its turns.
type Conversation interface {
	// Model reports the model and provider the conversation resolves to.
	Model(ctx context.Context) (model, provider string, err error)
	// AgentHandle is the runtime handle backing the conversation's turns, or
	// "" before its first turn. Spans carrying it are this conversation's.
	AgentHandle() string
	// Prompt runs one turn to completion, returning ErrInterrupted when
	// Interrupt preempted it.
	Prompt(ctx context.Context, input string) error
	// Steer offers a message to the in-flight turn, reporting whether there
	// was one to absorb it.
	Steer(input string) bool
	// Interrupt preempts the in-flight turn, reporting whether there was one.
	Interrupt() bool
	// LastReply is the assistant's final text from the last turn.
	LastReply(ctx context.Context) (string, error)
	// History renders the conversation so far as protocol turns.
	History(ctx context.Context) ([]Turn, error)
	// PendingDiff is the unified diff of workspace edits not yet exported to
	// the host checkout.
	PendingDiff(ctx context.Context) (string, error)
	// ExportChanges writes those edits to the host checkout.
	ExportChanges(ctx context.Context) error
	// TokenUsage reports cumulative usage and the model's context window (0
	// when unknown).
	TokenUsage(ctx context.Context) (TokenUsageBreakdown, int64, error)
	// SetName records the conversation's display name.
	SetName(name string)
}

// Server speaks the protocol over one connection.
type Server struct {
	conn    *Conn
	backend Backend
	stream  *ItemStream
	now     func() time.Time
	newID   func() string

	// drainQuiet and drainTimeout bound how long a finished turn waits for
	// its trailing telemetry before turn/completed goes out.
	drainQuiet   time.Duration
	drainTimeout time.Duration

	mu          sync.Mutex
	initialized bool
	client      ClientInfo
	optOut      map[string]bool
	threads     map[string]*thread
	order       []string

	turns sync.WaitGroup
}

type thread struct {
	id        string
	conv      Conversation
	ephemeral bool
	createdAt time.Time
	model     string
	provider  string

	mu        sync.Mutex
	name      *string
	preview   string
	updatedAt time.Time
	active    *turnState
	queue     []*turnState
	usage     TokenUsageBreakdown
}

type turnState struct {
	id        string
	input     []UserInput
	text      string
	startedAt time.Time
}

const (
	defaultDrainQuiet   = 250 * time.Millisecond
	defaultDrainTimeout = 5 * time.Second
)

// NewServer wires a server to its connection, backend and item stream. The
// stream is passed in rather than created here because it must be tapping
// telemetry before the engine session that produces it starts, which is
// before a backend can exist.
func NewServer(conn *Conn, backend Backend, stream *ItemStream) *Server {
	s := &Server{
		conn:         conn,
		backend:      backend,
		stream:       stream,
		now:          time.Now,
		newID:        NewID,
		drainQuiet:   defaultDrainQuiet,
		drainTimeout: defaultDrainTimeout,
		optOut:       map[string]bool{},
		threads:      map[string]*thread{},
	}
	stream.SetResolver(s.resolveTurn)
	return s
}

// NewID mints a UUIDv7: time-ordered, like Codex's own ids.
func NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

// Serve handles messages until the client closes its end or ctx is
// canceled. Requests are handled one at a time, in order; turns run in the
// background and report through notifications.
func (s *Server) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type incoming struct {
		msg *Message
		err error
	}
	msgs := make(chan incoming)
	go func() {
		defer close(msgs)
		for {
			msg, err := s.conn.Read()
			select {
			case msgs <- incoming{msg, err}:
			case <-ctx.Done():
				return
			}
			if err != nil && !isParseError(err) {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case in, ok := <-msgs:
			if !ok {
				return nil
			}
			if in.err != nil {
				var rpcErr *RPCError
				if errors.As(in.err, &rpcErr) {
					_ = s.conn.ReplyError(nil, rpcErr)
					continue
				}
				if errors.Is(in.err, io.EOF) {
					return nil
				}
				return in.err
			}
			s.handle(ctx, in.msg)
		}
	}
}

func isParseError(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == CodeParseError
}

func (s *Server) handle(ctx context.Context, msg *Message) {
	if !msg.IsRequest() {
		// The only client notification is "initialized", which needs no
		// action; anything else is a response to a request this server never
		// makes.
		return
	}
	result, err := s.dispatch(ctx, msg.Method, msg.Params)
	if err != nil {
		var rpcErr *RPCError
		if !errors.As(err, &rpcErr) {
			rpcErr = &RPCError{Code: CodeInternalError, Message: err.Error()}
		}
		_ = s.conn.ReplyError(msg.ID, rpcErr)
		return
	}
	_ = s.conn.Reply(msg.ID, result)
}

func (s *Server) dispatch(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == MethodInitialize {
		var p InitializeParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return s.initialize(p)
	}
	s.mu.Lock()
	initialized := s.initialized
	s.mu.Unlock()
	if !initialized {
		return nil, Errorf(CodeInvalidRequest, "server not initialized: send initialize first")
	}
	switch method {
	case MethodThreadStart:
		var p ThreadStartParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return s.threadStart(ctx, p)
	case MethodThreadResume:
		var p ThreadResumeParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return s.threadResume(ctx, p)
	case MethodThreadList:
		var p ThreadListParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return s.threadList(ctx, p)
	case MethodThreadLoadedList:
		return s.threadLoadedList(), nil
	case MethodThreadRead:
		var p ThreadReadParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return s.threadRead(ctx, p)
	case MethodThreadUnsubscribe:
		var p ThreadUnsubscribeParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		status := UnsubscribeStatusNotLoaded
		if s.thread(p.ThreadID) != nil {
			status = UnsubscribeStatusUnsubscribed
		}
		return ThreadUnsubscribeResponse{Status: status}, nil
	case MethodThreadNameSet:
		var p ThreadSetNameParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return s.threadSetName(p)
	case MethodTurnStart:
		var p TurnStartParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return s.turnStart(ctx, p)
	case MethodTurnSteer:
		var p TurnSteerParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return s.turnSteer(p)
	case MethodTurnInterrupt:
		var p TurnInterruptParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return s.turnInterrupt(p)
	case MethodModelList:
		var p ModelListParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return s.modelList(ctx, p)
	default:
		return nil, Errorf(CodeMethodNotFound, "method not found: %s", method)
	}
}

func decode(params json.RawMessage, into any) error {
	if len(params) == 0 || string(params) == "null" {
		return nil
	}
	if err := json.Unmarshal(params, into); err != nil {
		return Errorf(CodeInvalidParams, "invalid params: %s", err)
	}
	return nil
}

func (s *Server) initialize(p InitializeParams) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initialized {
		return nil, Errorf(CodeInvalidRequest, "server already initialized")
	}
	s.initialized = true
	s.client = p.ClientInfo
	if p.Capabilities != nil {
		for _, method := range p.Capabilities.OptOutNotificationMethods {
			s.optOut[method] = true
		}
	}
	info := s.backend.Info()
	family := "unix"
	if runtime.GOOS == "windows" {
		family = "windows"
	}
	return InitializeResponse{
		UserAgent: fmt.Sprintf("dagger/%s (%s; %s) %s/%s",
			info.Version, runtime.GOOS, runtime.GOARCH, p.ClientInfo.Name, p.ClientInfo.Version),
		CodexHome:      info.Home,
		PlatformFamily: family,
		PlatformOs:     runtime.GOOS,
	}, nil
}

// notify sends a notification unless the client opted out of its method.
func (s *Server) notify(method string, params any) {
	s.mu.Lock()
	skip := s.optOut[method]
	s.mu.Unlock()
	if skip {
		return
	}
	_ = s.conn.Notify(method, params)
}

// Notify implements Sink so the server can stand in for the connection.
func (s *Server) Notify(method string, params any) error {
	s.notify(method, params)
	return nil
}

func (s *Server) thread(id string) *thread {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threads[id]
}

func (s *Server) addThread(t *thread) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threads[t.id] = t
	s.order = append(s.order, t.id)
}

// resolveTurn is the item stream's resolver: which thread's active turn does
// the agent with this runtime handle serve?
func (s *Server) resolveTurn(agentID string) (TurnRef, bool) {
	s.mu.Lock()
	threads := make([]*thread, 0, len(s.threads))
	for _, t := range s.threads {
		threads = append(threads, t)
	}
	s.mu.Unlock()
	for _, t := range threads {
		if t.conv.AgentHandle() != agentID {
			continue
		}
		t.mu.Lock()
		active := t.active
		t.mu.Unlock()
		if active == nil {
			return TurnRef{}, false
		}
		return TurnRef{ThreadID: t.id, TurnID: active.id}, true
	}
	return TurnRef{}, false
}

func (s *Server) threadStart(ctx context.Context, p ThreadStartParams) (any, error) {
	opts := ThreadOptions{ID: s.newID()}
	if p.Model != nil {
		opts.Model = *p.Model
	}
	if p.ModelProvider != nil {
		opts.Provider = *p.ModelProvider
	}
	if p.Ephemeral != nil {
		opts.Ephemeral = *p.Ephemeral
	}
	var instructions []string
	for _, in := range []*string{p.BaseInstructions, p.DeveloperInstructions} {
		if in != nil && strings.TrimSpace(*in) != "" {
			instructions = append(instructions, *in)
		}
	}
	opts.Instructions = strings.Join(instructions, "\n\n")

	conv, err := s.backend.NewThread(ctx, opts)
	if err != nil {
		return nil, err
	}
	t, err := s.newThread(ctx, opts.ID, conv, opts.Ephemeral)
	if err != nil {
		return nil, err
	}
	s.addThread(t)
	proto := s.threadProto(t, []Turn{})
	s.notify(NotifyThreadStarted, ThreadStartedNotification{Thread: proto})
	return s.threadResponse(t, proto), nil
}

func (s *Server) newThread(ctx context.Context, id string, conv Conversation, ephemeral bool) (*thread, error) {
	model, provider, err := conv.Model(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	return &thread{
		id:        id,
		conv:      conv,
		ephemeral: ephemeral,
		createdAt: now,
		updatedAt: now,
		model:     model,
		provider:  provider,
	}, nil
}

func (s *Server) threadResponse(t *thread, proto Thread) ThreadStartResponse {
	info := s.backend.Info()
	return ThreadStartResponse{
		Thread:            proto,
		Model:             t.model,
		ModelProvider:     t.provider,
		Cwd:               info.Cwd,
		ApprovalPolicy:    ApprovalPolicyNever,
		ApprovalsReviewer: ApprovalsReviewerUser,
		Sandbox: SandboxPolicy{
			Type:          SandboxWorkspaceWrite,
			WritableRoots: []string{info.Cwd},
			NetworkAccess: true,
		},
	}
}

func (s *Server) threadProto(t *thread, turns []Turn) Thread {
	info := s.backend.Info()
	t.mu.Lock()
	defer t.mu.Unlock()
	status := ThreadStatus{Type: ThreadStatusIdle}
	if t.active != nil {
		status = ThreadStatus{Type: ThreadStatusActive, ActiveFlags: []string{}}
	}
	if turns == nil {
		turns = []Turn{}
	}
	return Thread{
		ID:            t.id,
		SessionID:     t.id,
		Preview:       t.preview,
		Name:          t.name,
		Ephemeral:     t.ephemeral,
		ModelProvider: t.provider,
		CreatedAt:     t.createdAt.Unix(),
		UpdatedAt:     t.updatedAt.Unix(),
		RecencyAt:     t.updatedAt.Unix(),
		Status:        status,
		Cwd:           info.Cwd,
		CliVersion:    info.Version,
		Source:        ThreadSourceAppServer,
		Turns:         turns,
	}
}

func (s *Server) threadResume(ctx context.Context, p ThreadResumeParams) (any, error) {
	if p.ThreadID == "" {
		return nil, Errorf(CodeInvalidParams, "threadId is required")
	}
	t := s.thread(p.ThreadID)
	if t == nil {
		conv, err := s.backend.LoadThread(ctx, p.ThreadID)
		if err != nil {
			return nil, Errorf(CodeInvalidRequest, "resume thread %s: %s", p.ThreadID, err)
		}
		t, err = s.newThread(ctx, p.ThreadID, conv, false)
		if err != nil {
			return nil, err
		}
		if saved, err := s.backend.SavedThreads(ctx); err == nil {
			for _, st := range saved {
				if st.ID == p.ThreadID {
					t.createdAt = st.CreatedAt
					t.updatedAt = st.CreatedAt
					if st.Name != "" {
						name := st.Name
						t.name = &name
						t.preview = name
					}
				}
			}
		}
		s.addThread(t)
	}
	turns, err := t.conv.History(ctx)
	if err != nil {
		slog.Warn("could not render thread history", "thread", t.id, "error", err)
		turns = nil
	}
	return s.threadResponse(t, s.threadProto(t, turns)), nil
}

func (s *Server) threadList(ctx context.Context, p ThreadListParams) (any, error) {
	saved, err := s.backend.SavedThreads(ctx)
	if err != nil {
		return nil, err
	}
	info := s.backend.Info()
	threads := make([]Thread, 0, len(saved))
	for _, st := range saved {
		if t := s.thread(st.ID); t != nil {
			threads = append(threads, s.threadProto(t, nil))
			continue
		}
		var name *string
		if st.Name != "" {
			n := st.Name
			name = &n
		}
		threads = append(threads, Thread{
			ID:            st.ID,
			SessionID:     st.ID,
			Preview:       st.Name,
			Name:          name,
			ModelProvider: "",
			CreatedAt:     st.CreatedAt.Unix(),
			UpdatedAt:     st.CreatedAt.Unix(),
			RecencyAt:     st.CreatedAt.Unix(),
			Status:        ThreadStatus{Type: ThreadStatusNotLoaded},
			Cwd:           info.Cwd,
			CliVersion:    info.Version,
			Source:        ThreadSourceAppServer,
			Turns:         []Turn{},
		})
	}
	// Loaded ephemeral threads are not on disk; list them too so a client
	// can find its way back to one it started.
	s.mu.Lock()
	for _, id := range s.order {
		t := s.threads[id]
		if !t.ephemeral {
			continue
		}
		threads = append(threads, s.threadProtoLocked(t))
	}
	s.mu.Unlock()
	sort.SliceStable(threads, func(i, j int) bool {
		return threads[i].CreatedAt > threads[j].CreatedAt
	})
	if p.Limit != nil && *p.Limit >= 0 && *p.Limit < len(threads) {
		threads = threads[:*p.Limit]
	}
	return ThreadListResponse{Data: threads, NextCursor: nil}, nil
}

// threadProtoLocked is threadProto for callers already holding s.mu; it
// exists because threadProto must not be called under it (Info and the
// thread lock are fine, s.mu is not reentrant).
func (s *Server) threadProtoLocked(t *thread) Thread {
	return s.threadProto(t, nil)
}

func (s *Server) threadLoadedList() ThreadLoadedListResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := append([]string{}, s.order...)
	return ThreadLoadedListResponse{Data: ids, NextCursor: nil}
}

func (s *Server) threadRead(ctx context.Context, p ThreadReadParams) (any, error) {
	if p.ThreadID == "" {
		return nil, Errorf(CodeInvalidParams, "threadId is required")
	}
	if t := s.thread(p.ThreadID); t != nil {
		var turns []Turn
		if p.IncludeTurns {
			var err error
			turns, err = t.conv.History(ctx)
			if err != nil {
				return nil, err
			}
		}
		return ThreadReadResponse{Thread: s.threadProto(t, turns)}, nil
	}
	saved, err := s.backend.SavedThreads(ctx)
	if err != nil {
		return nil, err
	}
	for _, st := range saved {
		if st.ID != p.ThreadID {
			continue
		}
		var name *string
		if st.Name != "" {
			n := st.Name
			name = &n
		}
		turns := []Turn{}
		if p.IncludeTurns {
			turns, err = s.backend.SavedThreadHistory(ctx, st.ID)
			if err != nil {
				return nil, err
			}
		}
		info := s.backend.Info()
		return ThreadReadResponse{Thread: Thread{
			ID:         st.ID,
			SessionID:  st.ID,
			Preview:    st.Name,
			Name:       name,
			CreatedAt:  st.CreatedAt.Unix(),
			UpdatedAt:  st.CreatedAt.Unix(),
			RecencyAt:  st.CreatedAt.Unix(),
			Status:     ThreadStatus{Type: ThreadStatusNotLoaded},
			Cwd:        info.Cwd,
			CliVersion: info.Version,
			Source:     ThreadSourceAppServer,
			Turns:      turns,
		}}, nil
	}
	return nil, Errorf(CodeInvalidRequest, "thread not found: %s", p.ThreadID)
}

func (s *Server) threadSetName(p ThreadSetNameParams) (any, error) {
	t := s.thread(p.ThreadID)
	if t == nil {
		return nil, Errorf(CodeInvalidRequest, "thread not loaded: %s", p.ThreadID)
	}
	name := strings.TrimSpace(p.Name)
	t.mu.Lock()
	if name == "" {
		t.name = nil
	} else {
		t.name = &name
	}
	t.mu.Unlock()
	t.conv.SetName(name)
	var proto *string
	if name != "" {
		proto = &name
	}
	s.notify(NotifyThreadNameUpdated, ThreadNameUpdatedNotification{ThreadID: t.id, ThreadName: proto})
	return struct{}{}, nil
}

// inputText flattens a turn's input to the prompt the agent receives. Only
// text is supported: images and file mentions have no Dagger counterpart yet,
// and silently dropping them would misrepresent what the agent was asked.
func inputText(input []UserInput) (string, error) {
	var parts []string
	for _, in := range input {
		switch in.Type {
		case UserInputText:
			parts = append(parts, in.Text)
		default:
			return "", Errorf(CodeInvalidParams, "unsupported input type %q: only text input is supported", in.Type)
		}
	}
	text := strings.Join(parts, "\n")
	if strings.TrimSpace(text) == "" {
		return "", Errorf(CodeInvalidParams, "input is empty")
	}
	return text, nil
}

func (s *Server) turnStart(ctx context.Context, p TurnStartParams) (any, error) {
	t := s.thread(p.ThreadID)
	if t == nil {
		return nil, Errorf(CodeInvalidRequest, "thread not loaded: %s", p.ThreadID)
	}
	text, err := inputText(p.Input)
	if err != nil {
		return nil, err
	}
	tn := &turnState{id: s.newID(), input: p.Input, text: text}
	proto := Turn{ID: tn.id, Items: []ThreadItem{}, Status: TurnStatusInProgress}

	t.mu.Lock()
	if t.name == nil && t.preview == "" {
		// The first prompt names the thread, as it names a TUI session.
		t.preview = text
		name := previewName(text)
		t.name = &name
		t.conv.SetName(name)
	}
	if t.active != nil {
		// One turn at a time per conversation: a second turn/start waits its
		// turn rather than being refused, so a client can queue follow-ups.
		t.queue = append(t.queue, tn)
		t.mu.Unlock()
		return TurnStartResponse{Turn: proto}, nil
	}
	t.active = tn
	t.mu.Unlock()
	s.turns.Add(1)
	go s.runTurn(ctx, t, tn)
	return TurnStartResponse{Turn: proto}, nil
}

// previewName derives a thread name from its first prompt: the first line,
// trimmed to a title's length.
func previewName(text string) string {
	line := strings.TrimSpace(text)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	line = strings.Join(strings.Fields(line), " ")
	const maxRunes = 60
	runes := []rune(line)
	if len(runes) > maxRunes {
		line = strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
	}
	return line
}

// runTurn drives one turn: it announces it, emits the user's message, runs the
// prompt while the item stream narrates it, then settles the stream, exports
// the workspace edits, reports usage, and announces completion.
func (s *Server) runTurn(ctx context.Context, t *thread, tn *turnState) {
	defer s.turns.Done()
	defer s.nextTurn(ctx, t)

	tn.startedAt = s.now()
	startedAt := tn.startedAt.Unix()
	ref := TurnRef{ThreadID: t.id, TurnID: tn.id}

	s.notify(NotifyThreadStatusChanged, ThreadStatusChangedNotification{
		ThreadID: t.id, Status: ThreadStatus{Type: ThreadStatusActive, ActiveFlags: []string{}},
	})
	s.notify(NotifyTurnStarted, TurnStartedNotification{ThreadID: t.id, Turn: Turn{
		ID: tn.id, Items: []ThreadItem{}, Status: TurnStatusInProgress, StartedAt: &startedAt,
	}})
	s.emitUserMessage(ref, tn.input)

	s.stream.BeginTurn(ref)
	err := t.conv.Prompt(ctx, tn.text)
	saw := s.stream.EndTurn(ref, err == nil, s.drainQuiet, s.drainTimeout)

	status := TurnStatusCompleted
	var turnErr *TurnError
	switch {
	case err == nil:
		if !saw {
			// The reply's telemetry never arrived in time; the conversation
			// itself still has it.
			if reply, rerr := t.conv.LastReply(ctx); rerr == nil && strings.TrimSpace(reply) != "" {
				s.emitAgentMessage(ref, reply)
			}
		}
	case errors.Is(err, ErrInterrupted), errors.Is(err, context.Canceled):
		status = TurnStatusInterrupted
	default:
		status = TurnStatusFailed
		turnErr = &TurnError{Message: err.Error()}
		s.notify(NotifyError, ErrorNotification{ThreadID: t.id, TurnID: tn.id, Error: *turnErr})
	}

	if ctx.Err() == nil {
		s.publishChanges(ctx, t, ref)
		s.publishUsage(ctx, t, ref)
	}

	completedAt := s.now()
	t.mu.Lock()
	t.updatedAt = completedAt
	t.mu.Unlock()
	completedUnix := completedAt.Unix()
	durationMs := completedAt.Sub(tn.startedAt).Milliseconds()
	s.notify(NotifyTurnCompleted, TurnCompletedNotification{ThreadID: t.id, Turn: Turn{
		ID:          tn.id,
		Items:       []ThreadItem{},
		Status:      status,
		Error:       turnErr,
		StartedAt:   &startedAt,
		CompletedAt: &completedUnix,
		DurationMs:  &durationMs,
	}})
}

// nextTurn releases the thread and starts the next queued turn, if any.
func (s *Server) nextTurn(ctx context.Context, t *thread) {
	t.mu.Lock()
	t.active = nil
	var next *turnState
	if len(t.queue) > 0 {
		next = t.queue[0]
		t.queue = t.queue[1:]
		t.active = next
	}
	t.mu.Unlock()
	if next != nil && ctx.Err() == nil {
		s.turns.Add(1)
		go s.runTurn(ctx, t, next)
		return
	}
	s.notify(NotifyThreadStatusChanged, ThreadStatusChangedNotification{
		ThreadID: t.id, Status: ThreadStatus{Type: ThreadStatusIdle},
	})
}

func (s *Server) emitUserMessage(ref TurnRef, input []UserInput) {
	item := NewUserMessageItem(s.newID(), input)
	now := s.now().UnixMilli()
	s.notify(NotifyItemStarted, ItemStartedNotification{ThreadID: ref.ThreadID, TurnID: ref.TurnID, Item: item, StartedAtMs: now})
	s.notify(NotifyItemCompleted, ItemCompletedNotification{ThreadID: ref.ThreadID, TurnID: ref.TurnID, Item: item, CompletedAtMs: now})
}

func (s *Server) emitAgentMessage(ref TurnRef, text string) {
	id := s.newID()
	now := s.now().UnixMilli()
	s.notify(NotifyItemStarted, ItemStartedNotification{ThreadID: ref.ThreadID, TurnID: ref.TurnID, Item: NewAgentMessageItem(id, ""), StartedAtMs: now})
	s.notify(NotifyAgentMessageDelta, AgentMessageDeltaNotification{ThreadID: ref.ThreadID, TurnID: ref.TurnID, ItemID: id, Delta: text})
	s.notify(NotifyItemCompleted, ItemCompletedNotification{ThreadID: ref.ThreadID, TurnID: ref.TurnID, Item: NewAgentMessageItem(id, text), CompletedAtMs: now})
}

// publishChanges reports the turn's workspace edits as a diff and writes them
// to the host checkout. A client speaking this protocol expects files on disk
// to change as the agent works; there is no ctrl+s on the other end.
func (s *Server) publishChanges(ctx context.Context, t *thread, ref TurnRef) {
	diff, err := t.conv.PendingDiff(ctx)
	if err != nil {
		slog.Warn("could not compute workspace diff", "thread", t.id, "error", err)
		return
	}
	if strings.TrimSpace(diff) == "" {
		return
	}
	s.notify(NotifyTurnDiffUpdated, TurnDiffUpdatedNotification{ThreadID: t.id, TurnID: ref.TurnID, Diff: diff})
	if err := t.conv.ExportChanges(ctx); err != nil {
		slog.Warn("could not export workspace changes to the host checkout", "thread", t.id, "error", err)
	}
}

func (s *Server) publishUsage(ctx context.Context, t *thread, ref TurnRef) {
	total, window, err := t.conv.TokenUsage(ctx)
	if err != nil {
		slog.Debug("could not read token usage", "thread", t.id, "error", err)
		return
	}
	t.mu.Lock()
	last := usageDelta(total, t.usage)
	t.usage = total
	t.mu.Unlock()
	usage := ThreadTokenUsage{Total: total, Last: last}
	if window > 0 {
		usage.ModelContextWindow = &window
	}
	s.notify(NotifyThreadTokenUsageUpdated, ThreadTokenUsageUpdatedNotification{ThreadID: t.id, TurnID: ref.TurnID, TokenUsage: usage})
}

// usageDelta is the turn's own usage: cumulative now minus cumulative before,
// or the whole total when history was compacted and the count reset.
func usageDelta(now, before TokenUsageBreakdown) TokenUsageBreakdown {
	if now.TotalTokens < before.TotalTokens {
		return now
	}
	return TokenUsageBreakdown{
		InputTokens:           now.InputTokens - before.InputTokens,
		CachedInputTokens:     now.CachedInputTokens - before.CachedInputTokens,
		OutputTokens:          now.OutputTokens - before.OutputTokens,
		ReasoningOutputTokens: now.ReasoningOutputTokens - before.ReasoningOutputTokens,
		TotalTokens:           now.TotalTokens - before.TotalTokens,
	}
}

func (s *Server) turnSteer(p TurnSteerParams) (any, error) {
	t := s.thread(p.ThreadID)
	if t == nil {
		return nil, Errorf(CodeInvalidRequest, "thread not loaded: %s", p.ThreadID)
	}
	text, err := inputText(p.Input)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	active := t.active
	t.mu.Unlock()
	if active == nil || active.id != p.ExpectedTurnID {
		return nil, Errorf(CodeInvalidRequest, "turn %s is not the active turn of thread %s", p.ExpectedTurnID, p.ThreadID)
	}
	if !t.conv.Steer(text) {
		return nil, Errorf(CodeInvalidRequest, "thread %s has no turn in flight to steer", p.ThreadID)
	}
	s.emitUserMessage(TurnRef{ThreadID: t.id, TurnID: active.id}, p.Input)
	return TurnSteerResponse{TurnID: active.id}, nil
}

func (s *Server) turnInterrupt(p TurnInterruptParams) (any, error) {
	t := s.thread(p.ThreadID)
	if t == nil {
		return nil, Errorf(CodeInvalidRequest, "thread not loaded: %s", p.ThreadID)
	}
	t.mu.Lock()
	active := t.active
	if active != nil && active.id == p.TurnID {
		t.mu.Unlock()
		if !t.conv.Interrupt() {
			return nil, Errorf(CodeInvalidRequest, "turn %s has nothing to interrupt", p.TurnID)
		}
		return struct{}{}, nil
	}
	// A queued turn never started: drop it and report it interrupted.
	for i, queued := range t.queue {
		if queued.id != p.TurnID {
			continue
		}
		t.queue = append(t.queue[:i], t.queue[i+1:]...)
		t.mu.Unlock()
		s.notify(NotifyTurnCompleted, TurnCompletedNotification{ThreadID: t.id, Turn: Turn{
			ID: queued.id, Items: []ThreadItem{}, Status: TurnStatusInterrupted,
		}})
		return struct{}{}, nil
	}
	t.mu.Unlock()
	return nil, Errorf(CodeInvalidRequest, "turn %s is not in progress on thread %s", p.TurnID, p.ThreadID)
}

func (s *Server) modelList(ctx context.Context, p ModelListParams) (any, error) {
	models, err := s.backend.Models(ctx)
	if err != nil {
		return nil, err
	}
	if models == nil {
		models = []Model{}
	}
	if p.Limit != nil && *p.Limit >= 0 && *p.Limit < len(models) {
		models = models[:*p.Limit]
	}
	return ModelListResponse{Data: models, NextCursor: nil}, nil
}
