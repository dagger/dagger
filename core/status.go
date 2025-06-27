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

	Actor       string
	Internal    bool
	Reveal      bool
	Passthrough bool

	Query *Query

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
	cp := &s
	cp.Query = cp.Query.Clone()
	return cp
}

func (s *Status) WithActor(actor string) *Status {
	cp := s.Clone()
	cp.Actor = actor
	return cp
}

func (s *Status) WithInternal() *Status {
	cp := s.Clone()
	cp.Internal = true
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

func (s *Status) Start(ctx context.Context) *Status {
	return s.Query.StartSpan(ctx, s)
}

func (s *Status) InternalID() string {
	if s.Span == nil {
		return ""
	}
	return s.Span.SpanContext().SpanID().String()
}

func (s *Status) Opts() []trace.SpanStartOption {
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
