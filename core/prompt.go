package core

import "errors"

// PromptRequest is a question the engine asks the user on behalf of the
// calling code (see Server.Prompt): a yes/no answer, some text, a choice
// among a few, or a secret. Exactly one of Boolean, Text, Select and Secret
// is set.
//
// Who asks is never part of the request: the engine resolves it from the
// calling client (the agent, module and function), and asks the trusted
// client that owns the call, showing the question where the calling span
// is rendered.
type PromptRequest struct {
	// Kind names what is asked, e.g. "llm-help" or "workspace-checkpoint".
	// Frontends may style by it.
	Kind string
	// Title is a (very) brief title, as plain text; may be empty.
	Title string
	// Message is the question, as Markdown.
	Message string

	Boolean *PromptBoolean
	Text    *PromptText
	Select  *PromptSelect
	Secret  *PromptSecret
}

type PromptBoolean struct {
	// Default is the answer preselected for the user.
	Default bool
}

type PromptText struct {
	// Default is the text the input starts with.
	Default   string
	Multiline bool
}

type PromptSelect struct {
	Choices []PromptChoice
	// Default names the choice preselected for the user; required.
	Default string
	// BoolFallback, if set, lets a client predating selections answer
	// yes/no instead: yes picks the first choice, no the second.
	BoolFallback *[2]string
}

type PromptChoice struct {
	ID    string
	Label string
}

type PromptSecret struct {
	Multiline bool
}

// PromptAnswer is the answer to a PromptRequest, of the kind it asked for.
type PromptAnswer struct {
	Boolean bool
	Text    string
	Choice  string
	Secret  []byte
}

// ErrPromptNotInteractive is returned (wrapped) by Server.Prompt when the
// owning client cannot ask the user: it has no terminal, or predates the
// kind of question asked.
var ErrPromptNotInteractive = errors.New("owning client cannot prompt (not interactive)")
