package dagui

import (
	"iter"
	"slices"
)

// ErrorCauseBreadcrumb returns the trees of the ancestors of an error
// origin's tree that the breadcrumb leading to it names, below the row
// stopAt, nearest the origin first. It's the breadcrumb of an error origin
// rendered beneath the row it bubbled up to, for every frontend: see
// Span.ErrorCauseBreadcrumb for an origin no tree places.
//
// The breadcrumb abbreviates the path on its own terms, at every verbosity,
// rather than mirroring what the tree shows (which reveals a failed span's
// encapsulated children). It leaves out internal and passthrough ancestors,
// whatever the verbosity, and ones never received, which have no name to
// show. Every shown ancestor that encapsulates its work is a landmark, named
// for the work beneath it, as a module's load and its build are; the steps
// between landmarks, and between the innermost one and the origin, fold
// away. The ancestors above the outermost landmark are named as they are. An
// internal ancestor's encapsulation doesn't count, as it isn't shown.
func (row *TraceTree) ErrorCauseBreadcrumb(stopAt SpanID) []*TraceTree {
	return errorCauseBreadcrumb(func(yield func(*TraceTree) bool) {
		for p := row.Parent; p != nil; p = p.Parent {
			if !yield(p) {
				return
			}
		}
	}, func(t *TraceTree) *Span { return t.Span }, stopAt)
}

// ErrorCauseBreadcrumb returns the ancestors of an error origin that the
// breadcrumb leading to it names, below the span stopAt, nearest the origin
// first, by the rules of TraceTree.ErrorCauseBreadcrumb. It walks the span's
// parents, for an origin no tree places, e.g. beneath a collapsed row, or a
// frontend that renders spans rather than trees.
func (span *Span) ErrorCauseBreadcrumb(stopAt SpanID) []*Span {
	return errorCauseBreadcrumb(func(yield func(*Span) bool) {
		for p := span.ParentSpan; p != nil; p = p.ParentSpan {
			if !yield(p) {
				return
			}
		}
	}, func(s *Span) *Span { return s }, stopAt)
}

func errorCauseBreadcrumb[T any](ancestors iter.Seq[T], spanOf func(T) *Span, stopAt SpanID) []T {
	var crumbs []T
	for ancestor := range ancestors {
		span := spanOf(ancestor)
		if span.ID == stopAt {
			break
		}
		if !span.Received || span.IsInternal() || span.Passthrough {
			// Not a row of its own: an unreceived placeholder has no name,
			// call, or message to show.
			continue
		}
		if span.Encapsulate {
			// A landmark: the steps beneath it, up to the next landmark, fold.
			crumbs = slices.DeleteFunc(crumbs, func(c T) bool {
				return !spanOf(c).Encapsulate
			})
		}
		crumbs = append(crumbs, ancestor)
	}
	return crumbs
}
