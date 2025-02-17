package core

import (
	"context"

	"dagger.io/dagger/telemetry"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Span struct {
	Name string `field:"true" doc:"The name of the span."`

	Actor       string
	Internal    bool
	Reveal      bool
	Passthrough bool

	Query *Query

	Span trace.Span
}

func (*Span) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Span",
		NonNull:   true,
	}
}

func (*Span) TypeDescription() string {
	// TODO: rename to Task and come up with a nice description
	return "An OpenTelemetry span."
}

func (s Span) Clone() *Span {
	cp := &s
	cp.Query = cp.Query.Clone()
	return cp
}

func (s *Span) WithActor(actor string) *Span {
	cp := s.Clone()
	cp.Actor = actor
	return cp
}

func (s *Span) WithInternal() *Span {
	cp := s.Clone()
	cp.Internal = true
	return cp
}

func (s *Span) WithPassthrough() *Span {
	cp := s.Clone()
	cp.Passthrough = true
	return cp
}

func (s *Span) WithReveal() *Span {
	cp := s.Clone()
	cp.Reveal = true
	return cp
}

func (s *Span) Start(ctx context.Context) *Span {
	return s.Query.StartSpan(ctx, s)
}

func (s *Span) InternalID() string {
	if s.Span == nil {
		return ""
	}
	return s.Span.SpanContext().SpanID().String()
}

func (s *Span) Opts() []trace.SpanStartOption {
	var opts []trace.SpanStartOption
	if s.Actor != "" {
		opts = append(opts, trace.WithAttributes(attribute.String("dagger.io/ui.actor", s.Actor)))
	}
	if s.Internal {
		opts = append(opts, telemetry.Internal())
	}
	if s.Reveal {
		opts = append(opts, trace.WithAttributes(attribute.Bool(telemetry.UIRevealAttr, true)))
	}
	if s.Passthrough {
		opts = append(opts, trace.WithAttributes(attribute.Bool(telemetry.UIPassthroughAttr, true)))
	}
	return opts
}
