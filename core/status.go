package core

import (
	"context"

	"dagger.io/dagger/telemetry"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Status struct {
	Name string `field:"true" doc:"The display name of the status."`

	ActorEmoji  string
	Message     string
	Reveal      bool
	Passthrough bool

	Span trace.Span
}

func (*Status) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Status",
		NonNull:   true,
	}
}

func (*Status) TypeDescription() string {
	return "A status indicator to show to the user."
}

func (s Status) Clone() *Status {
	return &s
}

func (s *Status) WithActorEmoji(actor string) *Status {
	cp := s.Clone()
	cp.ActorEmoji = actor
	return cp
}

func (s *Status) WithMessage(message string) *Status {
	cp := s.Clone()
	cp.Message = message
	return cp
}

func (s *Status) WithPassthrough() *Status {
	cp := s.Clone()
	cp.Passthrough = true
	return cp
}

func (s *Status) WithReveal() *Status {
	cp := s.Clone()
	cp.Reveal = true
	return cp
}

func (s *Status) Display(ctx context.Context) (*Status, error) {
	status, err := s.Start(ctx)
	if err != nil {
		return nil, err
	}
	// UNSET should be ok, it's not like failure is even possible
	status.Span.End()
	return status, nil
}

func (s *Status) Start(ctx context.Context) (*Status, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	return query.StartStatus(ctx, s)
}

func (s *Status) InternalID() string {
	if s.Span == nil {
		return ""
	}
	return s.Span.SpanContext().SpanID().String()
}

func (s *Status) Opts() []trace.SpanStartOption {
	var opts []trace.SpanStartOption
	if s.ActorEmoji != "" {
		opts = append(opts, trace.WithAttributes(attribute.String(telemetry.UIActorEmojiAttr, s.ActorEmoji)))
	}
	if s.Message != "" {
		opts = append(opts, trace.WithAttributes(attribute.String(telemetry.UIMessageAttr, s.Message)))
	}
	if s.Reveal {
		opts = append(opts, trace.WithAttributes(attribute.Bool(telemetry.UIRevealAttr, true)))
	}
	if s.Passthrough {
		opts = append(opts, trace.WithAttributes(attribute.Bool(telemetry.UIPassthroughAttr, true)))
	}
	return opts
}
