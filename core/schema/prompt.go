package schema

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
)

type promptSchema struct{}

var _ SchemaResolvers = &promptSchema{}

func (s *promptSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("prompt", s.prompt).
			Doc(`Ask the user a question.`,
				`Nothing is asked until an answer is selected from the returned Prompt, in the terminal of the client running the session, which shows who asks: the agent, module and function, as the engine knows them.`,
				`Answers are remembered for the session, per asking module, message and kind of answer.`).
			Args(
				dagql.Arg("message").Doc(`The question, as Markdown.`),
				dagql.Arg("key").Doc(
					`Remember answers beyond the session under this key, in the user's configuration (prompt-answers.json in Dagger's configuration directory, where they can be removed), for the asking module only. The user is told so when asked.`,
					`Only modules loaded from git are remembered so; secrets never are.`,
				),
			),
	}.Install(srv)

	dagql.Fields[*core.Prompt]{
		dagql.NodeFunc("boolean", s.boolean).
			WithInput(dagql.PerCallInput).
			Doc(`Ask for a yes or no answer.`).
			Args(
				dagql.Arg("default").Doc(
					`The answer selected to begin with, and the answer when nobody can be asked (the client is not interactive). Without one, that is an error.`,
				),
			),
		dagql.NodeFunc("string", s.string).
			WithInput(dagql.PerCallInput).
			Doc(`Ask for a text answer.`).
			Args(
				dagql.Arg("default").Doc(
					`The text the answer starts with, and the answer when nobody can be asked (the client is not interactive). Without one, that is an error.`,
				),
				dagql.Arg("multiline").Doc(`Accept several lines.`),
			),
		dagql.NodeFunc("secret", s.secret).
			WithInput(dagql.PerCallInput).
			Doc(`Ask for a secret value, masking the user's input.`,
				`The answer is never remembered beyond the session. When nobody can be asked (the client is not interactive), this is an error.`).
			Args(
				dagql.Arg("multiline").Doc(`Accept several lines.`),
			),
	}.Install(srv)
}

type promptArgs struct {
	Message string
	Key     dagql.Optional[dagql.String]
}

func (s *promptSchema) prompt(ctx context.Context, parent *core.Query, args promptArgs) (*core.Prompt, error) {
	if args.Message == "" {
		return nil, errors.New("prompt requires a message")
	}
	p := &core.Prompt{Message: args.Message}
	if args.Key.Valid {
		p.Key = args.Key.Value.String()
	}
	return p, nil
}

// ask asks the user p, with the answer kind req carries, through the
// engine's prompt broker (see Server.Prompt).
func ask(ctx context.Context, p *core.Prompt, req *core.PromptRequest) (*core.PromptAnswer, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	req.Kind = "prompt"
	req.Message = p.Message
	req.Remember = true
	req.Key = p.Key
	return query.Prompt(ctx, req)
}

// notAsked is the error for a prompt nobody could answer and that has no
// default to fall back to.
func notAsked(err error) error {
	if errors.Is(err, core.ErrPromptNotInteractive) {
		return fmt.Errorf("prompt needs an answer and has no default: %w", err)
	}
	return err
}

type promptBooleanArgs struct {
	Default dagql.Optional[dagql.Boolean]
}

func (s *promptSchema) boolean(ctx context.Context, parent dagql.ObjectResult[*core.Prompt], args promptBooleanArgs) (dagql.Boolean, error) {
	answer, err := ask(ctx, parent.Self(), &core.PromptRequest{
		Boolean: &core.PromptBoolean{Default: args.Default.Valid && args.Default.Value.Bool()},
	})
	if err != nil {
		if errors.Is(err, core.ErrPromptNotInteractive) && args.Default.Valid {
			return args.Default.Value, nil
		}
		return false, notAsked(err)
	}
	return dagql.NewBoolean(answer.Boolean), nil
}

type promptStringArgs struct {
	Default   dagql.Optional[dagql.String]
	Multiline bool `default:"false"`
}

func (s *promptSchema) string(ctx context.Context, parent dagql.ObjectResult[*core.Prompt], args promptStringArgs) (dagql.String, error) {
	text := &core.PromptText{Multiline: args.Multiline}
	if args.Default.Valid {
		text.Default = args.Default.Value.String()
	}
	answer, err := ask(ctx, parent.Self(), &core.PromptRequest{Text: text})
	if err != nil {
		if errors.Is(err, core.ErrPromptNotInteractive) && args.Default.Valid {
			return args.Default.Value, nil
		}
		return "", notAsked(err)
	}
	return dagql.NewString(answer.Text), nil
}

type promptSecretArgs struct {
	Multiline bool `default:"false"`
}

// secret asks for a secret and mints it from the answer, held in the engine
// like a secret the owner approved reading from its host (see
// Query.secret): its plaintext is never asked of a client again, and its
// handle derives from the value, so nothing else shares its cache entries.
func (s *promptSchema) secret(ctx context.Context, parent dagql.ObjectResult[*core.Prompt], args promptSecretArgs) (inst dagql.ObjectResult[*core.Secret], _ error) {
	answer, err := ask(ctx, parent.Self(), &core.PromptRequest{
		Secret: &core.PromptSecret{Multiline: args.Multiline},
	})
	if err != nil {
		return inst, notAsked(err)
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dagql server: %w", err)
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata from context: %w", err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dagql cache: %w", err)
	}
	handle := core.SecretHandleFromPlaintext(query.SecretSalt(), answer.Secret)
	if handle == "" {
		return inst, fmt.Errorf("secret must have a session resource handle")
	}
	res, err := dagql.NewObjectResultForCurrentCall(ctx, srv, &core.Secret{Handle: handle})
	if err != nil {
		return inst, err
	}
	res, err = res.WithContentDigest(ctx, digest.Digest(handle))
	if err != nil {
		return inst, err
	}
	res, err = res.WithSessionResourceHandle(ctx, handle)
	if err != nil {
		return inst, err
	}
	held := &core.Secret{PlaintextVal: answer.Secret, PlaintextHeld: true}
	if err := cache.BindSessionResource(ctx, clientMetadata.SessionID, clientMetadata.ClientID, handle, held); err != nil {
		return inst, fmt.Errorf("failed to bind prompted secret: %w", err)
	}
	return res, nil
}
