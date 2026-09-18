package codexapp

import "encoding/json"

// The subset of the Codex app-server v2 protocol this server implements. Field
// names and enum tokens follow the reference schema (`codex app-server
// generate-json-schema`) exactly; the shapes below carry only the members a
// Dagger-backed server can populate honestly.

// Method names, client to server.
const (
	MethodInitialize        = "initialize"
	MethodInitialized       = "initialized"
	MethodThreadStart       = "thread/start"
	MethodThreadResume      = "thread/resume"
	MethodThreadList        = "thread/list"
	MethodThreadLoadedList  = "thread/loaded/list"
	MethodThreadRead        = "thread/read"
	MethodThreadUnsubscribe = "thread/unsubscribe"
	MethodThreadNameSet     = "thread/name/set"
	MethodTurnStart         = "turn/start"
	MethodTurnSteer         = "turn/steer"
	MethodTurnInterrupt     = "turn/interrupt"
	MethodModelList         = "model/list"
)

// Notification method names, server to client.
const (
	NotifyThreadStarted           = "thread/started"
	NotifyThreadStatusChanged     = "thread/status/changed"
	NotifyThreadNameUpdated       = "thread/name/updated"
	NotifyThreadTokenUsageUpdated = "thread/tokenUsage/updated"
	NotifyTurnStarted             = "turn/started"
	NotifyTurnCompleted           = "turn/completed"
	NotifyTurnDiffUpdated         = "turn/diff/updated"
	NotifyItemStarted             = "item/started"
	NotifyItemCompleted           = "item/completed"
	NotifyAgentMessageDelta       = "item/agentMessage/delta"
	NotifyReasoningSummaryPart    = "item/reasoning/summaryPartAdded"
	NotifyReasoningSummaryDelta   = "item/reasoning/summaryTextDelta"
	NotifyError                   = "error"
)

// ClientInfo identifies the client in the initialize handshake.
type ClientInfo struct {
	Name    string  `json:"name"`
	Title   *string `json:"title,omitempty"`
	Version string  `json:"version"`
}

// InitializeCapabilities are the client-declared capabilities negotiated during
// initialize. Only the notification opt-out is honoured.
type InitializeCapabilities struct {
	ExperimentalAPI           bool     `json:"experimentalApi,omitempty"`
	OptOutNotificationMethods []string `json:"optOutNotificationMethods,omitempty"`
}

type InitializeParams struct {
	ClientInfo   ClientInfo              `json:"clientInfo"`
	Capabilities *InitializeCapabilities `json:"capabilities,omitempty"`
}

type InitializeResponse struct {
	UserAgent      string `json:"userAgent"`
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOs     string `json:"platformOs"`
}

// ThreadStatus is a thread's runtime status: notLoaded, idle, active or
// systemError. activeFlags is only present on an active thread.
type ThreadStatus struct {
	Type        string
	ActiveFlags []string
}

const (
	ThreadStatusNotLoaded = "notLoaded"
	ThreadStatusIdle      = "idle"
	ThreadStatusActive    = "active"
)

func (s ThreadStatus) MarshalJSON() ([]byte, error) {
	if s.Type == ThreadStatusActive {
		flags := s.ActiveFlags
		if flags == nil {
			flags = []string{}
		}
		return json.Marshal(struct {
			Type        string   `json:"type"`
			ActiveFlags []string `json:"activeFlags"`
		}{s.Type, flags})
	}
	return json.Marshal(struct {
		Type string `json:"type"`
	}{s.Type})
}

func (s *ThreadStatus) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type        string   `json:"type"`
		ActiveFlags []string `json:"activeFlags"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Type = raw.Type
	s.ActiveFlags = raw.ActiveFlags
	return nil
}

// Thread is a conversation. Turns are only populated on thread/resume and
// thread/read (includeTurns); everywhere else the list is empty.
type Thread struct {
	ID            string       `json:"id"`
	SessionID     string       `json:"sessionId"`
	Preview       string       `json:"preview"`
	Name          *string      `json:"name"`
	Ephemeral     bool         `json:"ephemeral"`
	ModelProvider string       `json:"modelProvider"`
	CreatedAt     int64        `json:"createdAt"`
	UpdatedAt     int64        `json:"updatedAt"`
	RecencyAt     int64        `json:"recencyAt"`
	Status        ThreadStatus `json:"status"`
	Cwd           string       `json:"cwd"`
	CliVersion    string       `json:"cliVersion"`
	Source        string       `json:"source"`
	Turns         []Turn       `json:"turns"`
}

// ThreadSourceAppServer is the SessionSource token for threads this server
// creates.
const ThreadSourceAppServer = "appServer"

// TurnStatus is one of completed, interrupted, failed or inProgress.
type TurnStatus string

const (
	TurnStatusCompleted   TurnStatus = "completed"
	TurnStatusInterrupted TurnStatus = "interrupted"
	TurnStatusFailed      TurnStatus = "failed"
	TurnStatusInProgress  TurnStatus = "inProgress"
)

// TurnError describes why a turn failed.
type TurnError struct {
	Message string `json:"message"`
}

// Turn is one exchange: the user's input and everything the agent did in
// response, as items.
type Turn struct {
	ID          string       `json:"id"`
	Items       []ThreadItem `json:"items"`
	Status      TurnStatus   `json:"status"`
	Error       *TurnError   `json:"error,omitempty"`
	StartedAt   *int64       `json:"startedAt,omitempty"`
	CompletedAt *int64       `json:"completedAt,omitempty"`
	DurationMs  *int64       `json:"durationMs,omitempty"`
}

// UserInput is one piece of a turn's input. Only text is supported; the other
// variants are decoded so they can be refused with a clear error.
type UserInput struct {
	Type         string `json:"type"`
	Text         string `json:"text"`
	TextElements []any  `json:"text_elements,omitempty"`
	URL          string `json:"url,omitempty"`
	Path         string `json:"path,omitempty"`
	Name         string `json:"name,omitempty"`
}

const UserInputText = "text"

// TextInput builds a text UserInput.
func TextInput(text string) UserInput {
	return UserInput{Type: UserInputText, Text: text}
}

// ThreadItem is one of the item structs below. They share the "type"
// discriminator the schema uses.
type ThreadItem any

const (
	ItemTypeUserMessage  = "userMessage"
	ItemTypeAgentMessage = "agentMessage"
	ItemTypeReasoning    = "reasoning"
	ItemTypeMcpToolCall  = "mcpToolCall"
)

type UserMessageItem struct {
	Type    string      `json:"type"`
	ID      string      `json:"id"`
	Content []UserInput `json:"content"`
}

func NewUserMessageItem(id string, content []UserInput) UserMessageItem {
	if content == nil {
		content = []UserInput{}
	}
	return UserMessageItem{Type: ItemTypeUserMessage, ID: id, Content: content}
}

type AgentMessageItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Text string `json:"text"`
}

func NewAgentMessageItem(id, text string) AgentMessageItem {
	return AgentMessageItem{Type: ItemTypeAgentMessage, ID: id, Text: text}
}

// ReasoningItem carries the model's thinking. Dagger has the full text, which
// goes in summary: that is the part every Codex client renders, while raw
// content is shown only when a client opts into it.
type ReasoningItem struct {
	Type    string   `json:"type"`
	ID      string   `json:"id"`
	Summary []string `json:"summary"`
	Content []string `json:"content"`
}

func NewReasoningItem(id string, summary []string) ReasoningItem {
	if summary == nil {
		summary = []string{}
	}
	return ReasoningItem{Type: ItemTypeReasoning, ID: id, Summary: summary, Content: []string{}}
}

const (
	ToolCallStatusInProgress = "inProgress"
	ToolCallStatusCompleted  = "completed"
	ToolCallStatusFailed     = "failed"
)

// McpToolCallItem is how every Dagger tool call is presented: the MCP tool
// call is the protocol's generic "named tool with JSON arguments and a text
// result" item, which is exactly what a Dagger tool is. Tools served by an
// MCP server the agent was given keep that server's name; the workspace's
// own tools are reported under the "dagger" server.
type McpToolCallItem struct {
	Type       string             `json:"type"`
	ID         string             `json:"id"`
	Server     string             `json:"server"`
	Tool       string             `json:"tool"`
	Arguments  any                `json:"arguments"`
	Status     string             `json:"status"`
	Result     *McpToolCallResult `json:"result,omitempty"`
	Error      *McpToolCallError  `json:"error,omitempty"`
	DurationMs *int64             `json:"durationMs,omitempty"`
}

// DaggerToolServer is the server name reported for the workspace's own tools.
const DaggerToolServer = "dagger"

type McpToolCallResult struct {
	Content []ToolResultContent `json:"content"`
}

type ToolResultContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type McpToolCallError struct {
	Message string `json:"message"`
}

// TextResult wraps a tool's text output as a result.
func TextResult(text string) *McpToolCallResult {
	return &McpToolCallResult{Content: []ToolResultContent{{Type: "text", Text: text}}}
}

// SandboxPolicy describes where the agent may write. Dagger agents edit a
// sandboxed copy of the workspace whose changes are exported to the checkout
// at the end of each turn, which is workspaceWrite from the client's point of
// view.
type SandboxPolicy struct {
	Type          string   `json:"type"`
	WritableRoots []string `json:"writableRoots,omitempty"`
	NetworkAccess bool     `json:"networkAccess"`
}

const (
	SandboxWorkspaceWrite = "workspaceWrite"
	ApprovalPolicyNever   = "never"
	ApprovalsReviewerUser = "user"
)

type ThreadStartParams struct {
	Model                 *string `json:"model,omitempty"`
	ModelProvider         *string `json:"modelProvider,omitempty"`
	Cwd                   *string `json:"cwd,omitempty"`
	BaseInstructions      *string `json:"baseInstructions,omitempty"`
	DeveloperInstructions *string `json:"developerInstructions,omitempty"`
	Ephemeral             *bool   `json:"ephemeral,omitempty"`
}

// ThreadStartResponse is also the shape of thread/resume's response.
type ThreadStartResponse struct {
	Thread            Thread        `json:"thread"`
	Model             string        `json:"model"`
	ModelProvider     string        `json:"modelProvider"`
	Cwd               string        `json:"cwd"`
	ApprovalPolicy    string        `json:"approvalPolicy"`
	ApprovalsReviewer string        `json:"approvalsReviewer"`
	Sandbox           SandboxPolicy `json:"sandbox"`
}

type ThreadResumeParams struct {
	ThreadID      string  `json:"threadId"`
	Model         *string `json:"model,omitempty"`
	ModelProvider *string `json:"modelProvider,omitempty"`
}

type ThreadListParams struct {
	Limit  *int    `json:"limit,omitempty"`
	Cursor *string `json:"cursor,omitempty"`
}

type ThreadListResponse struct {
	Data       []Thread `json:"data"`
	NextCursor *string  `json:"nextCursor"`
}

type ThreadLoadedListResponse struct {
	Data       []string `json:"data"`
	NextCursor *string  `json:"nextCursor"`
}

type ThreadReadParams struct {
	ThreadID     string `json:"threadId"`
	IncludeTurns bool   `json:"includeTurns,omitempty"`
}

type ThreadReadResponse struct {
	Thread Thread `json:"thread"`
}

type ThreadUnsubscribeParams struct {
	ThreadID string `json:"threadId"`
}

type ThreadUnsubscribeResponse struct {
	Status string `json:"status"`
}

const (
	UnsubscribeStatusNotLoaded    = "notLoaded"
	UnsubscribeStatusUnsubscribed = "unsubscribed"
)

type ThreadSetNameParams struct {
	ThreadID string `json:"threadId"`
	Name     string `json:"name"`
}

type TurnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []UserInput `json:"input"`
	Model    *string     `json:"model,omitempty"`
	Effort   *string     `json:"effort,omitempty"`
}

type TurnStartResponse struct {
	Turn Turn `json:"turn"`
}

type TurnSteerParams struct {
	ThreadID       string      `json:"threadId"`
	ExpectedTurnID string      `json:"expectedTurnId"`
	Input          []UserInput `json:"input"`
}

type TurnSteerResponse struct {
	TurnID string `json:"turnId"`
}

type TurnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type ModelListParams struct {
	Limit  *int    `json:"limit,omitempty"`
	Cursor *string `json:"cursor,omitempty"`
}

type ModelListResponse struct {
	Data       []Model `json:"data"`
	NextCursor *string `json:"nextCursor"`
}

type ReasoningEffortOption struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Description     string `json:"description"`
}

// Model is one entry of model/list.
type Model struct {
	ID                        string                  `json:"id"`
	Model                     string                  `json:"model"`
	DisplayName               string                  `json:"displayName"`
	Description               string                  `json:"description"`
	Hidden                    bool                    `json:"hidden"`
	IsDefault                 bool                    `json:"isDefault"`
	DefaultReasoningEffort    string                  `json:"defaultReasoningEffort"`
	SupportedReasoningEfforts []ReasoningEffortOption `json:"supportedReasoningEfforts"`
	InputModalities           []string                `json:"inputModalities"`
	SupportsPersonality       bool                    `json:"supportsPersonality"`
}

// Notifications.

type ThreadStartedNotification struct {
	Thread Thread `json:"thread"`
}

type ThreadStatusChangedNotification struct {
	ThreadID string       `json:"threadId"`
	Status   ThreadStatus `json:"status"`
}

type ThreadNameUpdatedNotification struct {
	ThreadID   string  `json:"threadId"`
	ThreadName *string `json:"threadName"`
}

type TurnStartedNotification struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

type TurnCompletedNotification struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

type TurnDiffUpdatedNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Diff     string `json:"diff"`
}

type ItemStartedNotification struct {
	ThreadID    string     `json:"threadId"`
	TurnID      string     `json:"turnId"`
	Item        ThreadItem `json:"item"`
	StartedAtMs int64      `json:"startedAtMs"`
}

type ItemCompletedNotification struct {
	ThreadID      string     `json:"threadId"`
	TurnID        string     `json:"turnId"`
	Item          ThreadItem `json:"item"`
	CompletedAtMs int64      `json:"completedAtMs"`
}

type AgentMessageDeltaNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

type ReasoningSummaryPartAddedNotification struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	SummaryIndex int64  `json:"summaryIndex"`
}

type ReasoningSummaryTextDeltaNotification struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	SummaryIndex int64  `json:"summaryIndex"`
	Delta        string `json:"delta"`
}

type TokenUsageBreakdown struct {
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
	TotalTokens           int64 `json:"totalTokens"`
}

type ThreadTokenUsage struct {
	Total              TokenUsageBreakdown `json:"total"`
	Last               TokenUsageBreakdown `json:"last"`
	ModelContextWindow *int64              `json:"modelContextWindow"`
}

type ThreadTokenUsageUpdatedNotification struct {
	ThreadID   string           `json:"threadId"`
	TurnID     string           `json:"turnId"`
	TokenUsage ThreadTokenUsage `json:"tokenUsage"`
}

type ErrorNotification struct {
	ThreadID  string    `json:"threadId"`
	TurnID    string    `json:"turnId"`
	Error     TurnError `json:"error"`
	WillRetry bool      `json:"willRetry"`
}
