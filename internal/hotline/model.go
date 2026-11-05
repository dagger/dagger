package hotline

import (
	"strings"
	"time"
)

const MaxBody = 64 * 1024

var Kinds = []string{"setup", "feedback", "question", "bug", "billing", "suggestion"}

// Principal comes from verified Cloud authentication, never from request JSON.
type Principal struct {
	OrgID   string
	ActorID string
	Team    bool
	Billing bool
}

type Thread struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Outcome   string    `json:"outcome,omitempty"`
	Workspace string    `json:"workspace,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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

// Input is shared by all six conversation types.
type Input struct {
	Attachments []File `json:"attachments,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Outcome     string `json:"outcome,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	Body        string `json:"body"`
}

type Conversation struct {
	Thread   Thread    `json:"thread"`
	Messages []Message `json:"messages"`
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
func HasKind(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}
func (in Input) Validate(create bool) error {
	if err := ValidateFiles(in.Attachments); err != nil {
		return err
	}
	if strings.TrimSpace(in.Body) == "" || len(in.Body) > MaxBody || strings.ContainsRune(in.Body, 0) {
		return problem(400, "invalid_input", "Supply a message of 1–65536 bytes without null characters.")
	}
	if !create {
		if in.Kind != "" || in.Outcome != "" || in.Workspace != "" {
			return problem(400, "invalid_input", "A reply accepts only a body.")
		}
		return nil
	}
	if !HasKind(in.Kind) {
		return problem(400, "invalid_input", "Choose setup, feedback, question, bug, billing, or suggestion.")
	}
	if len(in.Workspace) > 512 || strings.ContainsRune(in.Workspace, 0) {
		return problem(400, "invalid_input", "Use a workspace name of at most 512 bytes without null characters.")
	}
	if in.Kind == "setup" {
		if in.Outcome != "success" && in.Outcome != "blocked" && in.Outcome != "stopped" {
			return problem(400, "invalid_input", "Setup requires --outcome success, blocked, or stopped.")
		}
	} else if in.Outcome != "" {
		return problem(400, "invalid_input", "Only setup accepts an outcome.")
	}
	return nil
}
