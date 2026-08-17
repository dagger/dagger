package dagui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// How much of a rendered literal to keep. An argument can carry a whole file's
// contents and this ends up in an error message, so every unbounded lane --
// string length, list length, argument count -- gets a cap.
const (
	maxLiteralLen   = 48
	maxLiteralElems = 4
)

// frameRef says which frame referenced a call, and how.
//
// Both halves matter for a gap report. The relation distinguishes a hole on
// the receiver spine from one behind an ID-literal argument, which have
// different causes and different fixes; the call itself is what makes the
// referrer findable in the trace.
type frameRef struct {
	// call is the referring frame. Nil for the root of the walk, which
	// nothing referenced.
	call *callpbv1.Call
	// rel is how the referrer reached the missing call, e.g.
	//	receiver
	//	argument "object"
	//	module
	rel string
}

func (r frameRef) String() string {
	if r.call == nil {
		return ""
	}
	return fmt.Sprintf("%s of %s", r.rel, frameDetail(r.call))
}

// missingCall records a call the walk could not resolve, together with the
// frame that referenced it.
//
// The referrer is the whole point. extractIntoDAG only ever visits digests
// something points at, so a gap is always reachable from some frame -- and
// without naming that frame the eventual failure is a bare digest the reader
// has no way to look up, on a chain that can be a dozen frames deep. The
// missing call itself survives only as that digest: nothing in this client
// knows its field, its type or its receiver, because the payload is precisely
// what never arrived. So the referrer is the only description of the gap that
// can ever be printed, and it is printed in full -- digest and arguments --
// rather than by field and type alone, which narrows a live failure to "some
// `directory` call" and leaves the rest to hypothesis.
type missingCall struct {
	digest string
	ref    frameRef
}

func (m missingCall) String() string {
	if m.ref.call == nil {
		return fmt.Sprintf("call %s never reached this client", m.digest)
	}
	return fmt.Sprintf("call %s never reached this client, referenced as %s",
		m.digest, m.ref)
}

// extractIntoDAG recursively populates recipe.CallsByDigest from the call and
// its dependencies, and returns the references it could not resolve.
//
// Unresolvable references are reported rather than raised: a partial recipe is
// still worth rendering, and only callers that need a loadable ID care. Those
// callers must check, because the alternative is what this used to do —
// silently omit the frame and let call.ID.decode fail much later with
// `call digest %q not found`, wrapped once per frame it unwound through,
// naming the digest but never the frame that wanted it. This is the last place
// that still knows the context, so it is where the context gets recorded.
func extractIntoDAG(recipe *callpbv1.RecipeDAG, db *DB, callDigest string) []missingCall {
	x := &dagExtractor{recipe: recipe, db: db}
	x.extractCall(callDigest, frameRef{})
	return x.missing
}

type dagExtractor struct {
	recipe  *callpbv1.RecipeDAG
	db      *DB
	missing []missingCall
}

func (x *dagExtractor) extractCall(callDigest string, via frameRef) {
	if callDigest == "" {
		return
	}
	if _, exists := x.recipe.CallsByDigest[callDigest]; exists {
		return
	}

	call := x.db.Call(callDigest)
	if call == nil {
		x.missing = append(x.missing, missingCall{digest: callDigest, ref: via})
		return
	}
	call = &callpbv1.Call{
		ReceiverDigest: call.ReceiverDigest,
		Type:           call.Type,
		Field:          call.Field,
		Args:           call.Args,
		Nth:            call.Nth,
		Module:         call.Module,
		Digest:         callDigest,
		View:           call.View,
	}
	x.recipe.CallsByDigest[callDigest] = call

	if call.ReceiverDigest != "" {
		x.extractCall(call.ReceiverDigest, frameRef{call: call, rel: "receiver"})
	}
	for _, arg := range call.Args {
		if arg.Value != nil {
			x.extractLit(arg.Value, frameRef{
				call: call,
				rel:  fmt.Sprintf("argument %q", arg.GetName()),
			})
		}
	}
	if call.Module != nil && call.Module.CallDigest != "" {
		x.extractCall(call.Module.CallDigest, frameRef{call: call, rel: "module"})
	}
}

// extractLit recursively extracts calls from literals, carrying the referring
// frame down so a call buried in a list or object argument still reports where
// it came from.
func (x *dagExtractor) extractLit(lit *callpbv1.Literal, via frameRef) {
	switch v := lit.Value.(type) {
	case *callpbv1.Literal_CallDigest:
		x.extractCall(v.CallDigest, via)
	case *callpbv1.Literal_List:
		if v.List != nil {
			for _, val := range v.List.Values {
				x.extractLit(val, via)
			}
		}
	case *callpbv1.Literal_Object:
		if v.Object != nil {
			for _, val := range v.Object.Values {
				if val.Value != nil {
					x.extractLit(val.Value, via)
				}
			}
		}
	default:
		// Other literal types do not reference calls, so ignore.
	}
}

// frameLabel names a call the way a reader can find it in the trace: the field
// selected, and the type it returned.
func frameLabel(call *callpbv1.Call) string {
	field := call.GetField()
	if field == "" {
		field = "?"
	}
	if typ := call.GetType().GetNamedType(); typ != "" {
		return fmt.Sprintf("%q (%s)", field, typ)
	}
	return fmt.Sprintf("%q", field)
}

// frameDetail identifies a call precisely enough to go find it: its label,
// its own digest, and the arguments it was selected with.
//
// The label alone does not identify a frame -- a chain can select `directory`
// half a dozen times -- and it says nothing about what the frame was applied
// to. The digest makes the frame greppable in the trace, and the arguments
// usually identify the missing receiver by inspection, which is the difference
// between reading the answer off the error and guessing at it.
func frameDetail(call *callpbv1.Call) string {
	detail := frameLabel(call)
	if dgst := call.GetDigest(); dgst != "" {
		detail += " " + dgst
	}
	return detail + "(" + frameArgs(call) + ")"
}

// frameArgs renders a call's arguments compactly, bounded on every axis.
func frameArgs(call *callpbv1.Call) string {
	return joinArgs(call.GetArgs())
}

func joinArgs(args []*callpbv1.Argument) string {
	parts := make([]string, 0, len(args))
	for i, arg := range args {
		if i == maxLiteralElems {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, fmt.Sprintf("%s: %s", arg.GetName(), literalLabel(arg.GetValue())))
	}
	return strings.Join(parts, ", ")
}

// literalLabel renders a literal for a diagnostic, not for replay: an
// ID-literal shows as the digest it references, so a gap behind an argument
// can be traced to the argument that holds it.
func literalLabel(lit *callpbv1.Literal) string {
	switch v := lit.GetValue().(type) {
	case *callpbv1.Literal_CallDigest:
		return v.CallDigest
	case *callpbv1.Literal_Null:
		return "null"
	case *callpbv1.Literal_Bool:
		return strconv.FormatBool(v.Bool)
	case *callpbv1.Literal_Enum:
		return v.Enum
	case *callpbv1.Literal_Int:
		return strconv.FormatInt(v.Int, 10)
	case *callpbv1.Literal_Float:
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	case *callpbv1.Literal_String_:
		return strconv.Quote(truncateLiteral(v.String_))
	case *callpbv1.Literal_DigestedString:
		return strconv.Quote(truncateLiteral(v.DigestedString.GetValue()))
	case *callpbv1.Literal_List:
		vals := v.List.GetValues()
		parts := make([]string, 0, len(vals))
		for i, val := range vals {
			if i == maxLiteralElems {
				parts = append(parts, "…")
				break
			}
			parts = append(parts, literalLabel(val))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *callpbv1.Literal_Object:
		return "{" + joinArgs(v.Object.GetValues()) + "}"
	default:
		// Includes a nil literal, which an argument is allowed to carry.
		return "?"
	}
}

func truncateLiteral(s string) string {
	if len(s) <= maxLiteralLen {
		return s
	}
	return s[:maxLiteralLen] + "…"
}
