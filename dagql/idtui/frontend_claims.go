package idtui

import (
	"github.com/dagger/dagger/dagql/dagui"
	telemetry "github.com/dagger/otel-go"
)

type renderClaims struct {
	errors map[dagui.SpanID]struct{}
}

func newRenderClaims() *renderClaims {
	return &renderClaims{
		errors: make(map[dagui.SpanID]struct{}),
	}
}

func (claims *renderClaims) claimError(span *dagui.Span) {
	if span == nil {
		return
	}
	claims.claimErrorID(span.ID)
}

func (claims *renderClaims) claimErrorID(id dagui.SpanID) {
	if claims == nil || !id.IsValid() {
		return
	}
	claims.errors[id] = struct{}{}
}

func (claims *renderClaims) hasError(id dagui.SpanID) bool {
	if claims == nil || !id.IsValid() {
		return false
	}
	_, ok := claims.errors[id]
	return ok
}

func (claims *renderClaims) claimTestReport(span *dagui.Span) {
	if claims == nil || span == nil {
		return
	}
	for _, origin := range span.ErrorOrigins.Order {
		if origin == nil || origin.ID == span.ID {
			continue
		}
		claims.claimError(origin)
	}
}

func (claims *renderClaims) hasRootError(err error) bool {
	if claims == nil || err == nil {
		return false
	}
	origins := telemetry.ParseErrorOrigins(err.Error())
	if len(origins) == 0 {
		return false
	}
	for _, origin := range origins {
		if !origin.IsValid() {
			return false
		}
		if !claims.hasError(dagui.SpanID{SpanID: origin.SpanID()}) {
			return false
		}
	}
	return true
}
