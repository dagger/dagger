package hotline

import (
	"strings"
	"time"
)

const MaxBody = 64 * 1024

// Principal comes from verified Cloud authentication, never from request JSON.
type Principal struct {
	OrgID   string
	ActorID string
	Team    bool
}

type Thread struct {
	ID            int64     `json:"id"`
	Workspace     string    `json:"workspace,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	LastMessageID int64     `json:"last_message_id"`
	LastAuthorID  string    `json:"last_author_id"`
	LastTeam      bool      `json:"last_team"`
	Preview       string    `json:"preview"`
	UnreadCount   int       `json:"unread_count"`
	LastUnreadID  int64     `json:"last_unread_id"`
}

type Message struct {
	Attachments []Attachment `json:"attachments,omitempty"`
	ID          int64        `json:"id"`
	ThreadID    int64        `json:"thread_id"`
	AuthorID    string       `json:"author_id"`
	Team        bool         `json:"team"`
	Body        string       `json:"body"`
	CreatedAt   time.Time    `json:"created_at"`
}

type Input struct {
	Attachments []File `json:"attachments,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	Body        string `json:"body"`
}

type PageOptions struct {
	After  int64
	Limit  int
	Unread bool
}

func (o PageOptions) Validate() error {
	if o.After < 0 || o.Limit < 1 || o.Limit > 100 {
		return problem(400, "invalid_input", "Use --after 0 or a positive cursor, and --limit 1–100.")
	}
	return nil
}

type ThreadPage struct {
	Threads    []Thread `json:"threads"`
	NextCursor int64    `json:"next_cursor,omitempty"`
}

type Conversation struct {
	Thread     Thread    `json:"thread"`
	Messages   []Message `json:"messages"`
	After      int64     `json:"after"`
	NextCursor int64     `json:"next_cursor,omitempty"`
}

// ReadRange prevents a page that skips unread messages from clearing them.
type ReadRange struct {
	After   int64 `json:"after"`
	Through int64 `json:"through"`
}

type Sent struct {
	ThreadID  int64  `json:"thread_id"`
	MessageID int64  `json:"message_id"`
	RequestID string `json:"request_id"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func problem(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}
func (in Input) Validate(create bool) error {
	if err := ValidateFiles(in.Attachments); err != nil {
		return err
	}
	if strings.TrimSpace(in.Body) == "" || len(in.Body) > MaxBody || strings.ContainsRune(in.Body, 0) {
		return problem(400, "invalid_input", "Supply a message of 1–65536 bytes without null characters.")
	}
	if !create && in.Workspace != "" {
		return problem(400, "invalid_input", "Set the workspace only when you create a thread.")
	}
	if len(in.Workspace) > 512 || strings.ContainsRune(in.Workspace, 0) {
		return problem(400, "invalid_input", "Use a workspace name of at most 512 bytes without null characters.")
	}
	return nil
}
