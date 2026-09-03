package dagui

import (
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// A chain is only rebuildable if EVERY frame it references has a call payload
// in this client's DB, including the frames buried inside ID-literal
// arguments. When one is absent the walk used to drop it silently and let
// call.ID.decode fail later with a bare "call digest %q not found", wrapped
// once per frame it unwound through -- a wall of identical wrappers naming a
// digest, but never the frame that referenced it, on chains that in practice
// run a dozen frames deep.
func TestExtractIntoDAGReportsMissingFrames(t *testing.T) {
	// llm(...).withTools(object: <missing>.staff()).agent(...)
	//
	// The gap sits behind an argument rather than the receiver spine, which is
	// the case the bare-digest error was least able to explain.
	db := NewDB()
	db.Calls = map[string]*callpbv1.Call{
		"xxh3:root": {
			Digest:         "xxh3:root",
			Field:          "agent",
			Type:           &callpbv1.Type{NamedType: "Agent"},
			ReceiverDigest: "xxh3:withTools",
		},
		"xxh3:withTools": {
			Digest: "xxh3:withTools",
			Field:  "withTools",
			Type:   &callpbv1.Type{NamedType: "LLM"},
			Args: []*callpbv1.Argument{{
				Name: "object",
				Value: &callpbv1.Literal{
					Value: &callpbv1.Literal_CallDigest{CallDigest: "xxh3:staff"},
				},
			}},
		},
		"xxh3:staff": {
			Digest: "xxh3:staff",
			Field:  "staff",
			Type:   &callpbv1.Type{NamedType: "Staff"},
			Args: []*callpbv1.Argument{{
				Name:  "name",
				Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "scout"}},
			}},
			// Its receiver's span never reached this client.
			ReceiverDigest: "xxh3:gone",
		},
	}

	recipe := &callpbv1.RecipeDAG{
		RootDigest:    "xxh3:root",
		CallsByDigest: map[string]*callpbv1.Call{},
	}
	missing := extractIntoDAG(recipe, db, "xxh3:root")

	if len(missing) != 1 {
		t.Fatalf("expected exactly the one unresolvable reference, got %v", missing)
	}
	if missing[0].digest != "xxh3:gone" {
		t.Errorf("missing call digest = %q, want xxh3:gone", missing[0].digest)
	}
	// The relation is what tells a receiver-spine gap from one behind an
	// ID-literal argument -- two different bugs with two different fixes.
	if want := "receiver"; missing[0].ref.rel != want {
		t.Errorf("missing call relation = %q, want %q", missing[0].ref.rel, want)
	}
	if got := missing[0].ref.call.GetDigest(); got != "xxh3:staff" {
		t.Errorf("referring frame = %q, want xxh3:staff", got)
	}

	// The rendered report is the whole value of the exercise: the missing call
	// survives only as a digest, so the referrer has to be identified well
	// enough to go find it -- by digest, and by the arguments it was selected
	// with. Naming it by field and type alone ("some `staff` call") is what
	// left a live investigation with nothing to inspect.
	msg := missing[0].String()
	for _, want := range []string{
		"xxh3:gone",       // the gap itself
		"receiver of",     // how it was reached
		`"staff" (Staff)`, // the referring frame, by name
		"xxh3:staff",      // ... and by digest, so it is findable in the trace
		`name: "scout"`,   // ... with the arguments that identify its receiver
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("rendered message %q does not mention %q", msg, want)
		}
	}

	// Everything reachable is still collected: a gap degrades the result, it
	// does not abandon the walk.
	for _, dgst := range []string{"xxh3:root", "xxh3:withTools", "xxh3:staff"} {
		if _, ok := recipe.CallsByDigest[dgst]; !ok {
			t.Errorf("resolvable call %s missing from the recipe", dgst)
		}
	}
}

// The ordinary case must stay silent: a complete chain reports nothing, so
// callers can treat a non-empty result as a real failure.
func TestExtractIntoDAGCompleteChain(t *testing.T) {
	db := NewDB()
	db.Calls = map[string]*callpbv1.Call{
		"xxh3:root": {
			Digest:         "xxh3:root",
			Field:          "agent",
			Type:           &callpbv1.Type{NamedType: "Agent"},
			ReceiverDigest: "xxh3:llm",
		},
		"xxh3:llm": {
			Digest: "xxh3:llm",
			Field:  "llm",
			Type:   &callpbv1.Type{NamedType: "LLM"},
		},
	}

	recipe := &callpbv1.RecipeDAG{
		RootDigest:    "xxh3:root",
		CallsByDigest: map[string]*callpbv1.Call{},
	}
	if missing := extractIntoDAG(recipe, db, "xxh3:root"); len(missing) != 0 {
		t.Fatalf("complete chain reported missing frames: %v", missing)
	}
	if len(recipe.CallsByDigest) != 2 {
		t.Fatalf("expected both frames in the recipe, got %d", len(recipe.CallsByDigest))
	}
}

// The live failure this diagnostic was built for names a `directory` call, of
// which a real chain has several. What distinguishes them is their arguments,
// so those are rendered -- including an ID-literal argument, which renders as
// the digest it points at, and is how a gap behind an argument gets traced to
// the argument holding it.
func TestFrameDetailIdentifiesTheFrame(t *testing.T) {
	call := &callpbv1.Call{
		Digest: "xxh3:dir",
		Field:  "directory",
		Type:   &callpbv1.Type{NamedType: "Directory"},
		Args: []*callpbv1.Argument{
			{
				Name:  "path",
				Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "/src"}},
			},
			{
				Name: "include",
				Value: &callpbv1.Literal{Value: &callpbv1.Literal_List{
					List: &callpbv1.List{Values: []*callpbv1.Literal{
						{Value: &callpbv1.Literal_String_{String_: "**/*.go"}},
					}},
				}},
			},
			{
				Name:  "source",
				Value: &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: "xxh3:ws"}},
			},
			{
				Name:  "keep",
				Value: &callpbv1.Literal{Value: &callpbv1.Literal_Bool{Bool: true}},
			},
		},
	}

	want := `"directory" (Directory) xxh3:dir(path: "/src", include: ["**/*.go"], source: xxh3:ws, keep: true)`
	if got := frameDetail(call); got != want {
		t.Errorf("frameDetail =\n\t%s\nwant\n\t%s", got, want)
	}
}

// An argument can carry a whole file's contents, and this string ends up in an
// error message. Every unbounded lane is capped, and a nil literal -- which an
// argument is allowed to carry -- renders rather than panics.
func TestFrameDetailIsBounded(t *testing.T) {
	long := strings.Repeat("x", maxLiteralLen*4)
	many := make([]*callpbv1.Literal, maxLiteralElems*3)
	for i := range many {
		many[i] = &callpbv1.Literal{Value: &callpbv1.Literal_Int{Int: int64(i)}}
	}

	call := &callpbv1.Call{
		Digest: "xxh3:big",
		Field:  "withNewFile",
		Type:   &callpbv1.Type{NamedType: "Workspace"},
		Args: []*callpbv1.Argument{
			{
				Name:  "contents",
				Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: long}},
			},
			{
				Name: "paths",
				Value: &callpbv1.Literal{Value: &callpbv1.Literal_List{
					List: &callpbv1.List{Values: many},
				}},
			},
			{Name: "missing"},
			{Name: "a"},
			{Name: "b"},
			{Name: "c"},
		},
	}

	got := frameDetail(call)
	if strings.Contains(got, long) {
		t.Errorf("long string argument rendered in full: %q", got)
	}
	if strings.Contains(got, "c: ") {
		t.Errorf("argument list not capped: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("truncation left no marker: %q", got)
	}
	if !strings.Contains(got, "missing: ?") {
		t.Errorf("nil literal did not render: %q", got)
	}
	if n := len(got); n > 400 {
		t.Errorf("rendered detail is %d bytes, too long for an error message: %q", n, got)
	}
}

// The root of the walk is referenced by nothing, so its report has no referrer
// to name -- and must still say what happened rather than render an empty
// clause.
func TestMissingCallWithoutReferrer(t *testing.T) {
	db := NewDB()
	recipe := &callpbv1.RecipeDAG{
		RootDigest:    "xxh3:gone",
		CallsByDigest: map[string]*callpbv1.Call{},
	}
	missing := extractIntoDAG(recipe, db, "xxh3:gone")

	if len(missing) != 1 {
		t.Fatalf("expected the root to be reported missing, got %v", missing)
	}
	msg := missing[0].String()
	if !strings.Contains(msg, "xxh3:gone") {
		t.Errorf("report does not name the digest: %q", msg)
	}
	if strings.Contains(msg, "referenced as") {
		t.Errorf("report claims a referrer it does not have: %q", msg)
	}
}
