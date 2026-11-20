package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

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

	// Remember remembers the answer for the session, per owner, asking
	// module, message, answer kind and Key: the same question from the same
	// module is answered once. It also marks the calling function call as
	// depending on the answer, so its result is never reused for another
	// call (see FunctionCall.MarkPrompted).
	Remember bool
	// Key, with Remember, lets the owner's client remember the answer
	// beyond the session, under a key the engine namespaces by the asking
	// module's git source, so no module reads or plants another's answers.
	// Only modules loaded from git are identified well enough; others, and
	// secrets, are only remembered for the session.
	Key string
}

// Prompt is a question for the user, asked with Query.prompt; its fields ask
// for an answer of one kind.
type Prompt struct {
	// Message is the question, as Markdown.
	Message string
	// Key, if set, remembers answers beyond the session (see
	// PromptRequest.Key).
	Key string
}

func (*Prompt) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Prompt",
		NonNull:   true,
	}
}

func (*Prompt) TypeDescription() string {
	return dagql.FormatDescription(
		`A question for the user, answered in the terminal of the client running the session.`,
		`Select the kind of answer wanted to ask it: each selection asks, unless the answer is remembered.`,
	)
}

var _ dagql.PersistedObject = (*Prompt)(nil)
var _ dagql.PersistedObjectDecoder = (*Prompt)(nil)

type persistedPromptPayload struct {
	Message string `json:"message"`
	Key     string `json:"key,omitempty"`
}

func (p *Prompt) EncodePersistedObject(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if p == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted prompt: nil prompt")
	}
	return encodePersistedObjectPayload(persistedPromptPayload{Message: p.Message, Key: p.Key})
}

func (*Prompt) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedPromptPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted prompt payload: %w", err)
	}
	return &Prompt{Message: persisted.Message, Key: persisted.Key}, nil
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
