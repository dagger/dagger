package core

import (
	"context"

	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"
)

type Span struct {
	Name     string `field:"true" doc:"The name of the span."`
	Actor    string `field:"true" doc:"An optional actor to display for the span."`
	Internal bool   `field:"true" doc:"Indicates that the span contains details that are not important to the user in the happy path."`
	Reveal   bool   `field:"true" doc:"Indicates that the span should be revealed in the UI."`

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

func (s *Span) Revealed() *Span {
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
