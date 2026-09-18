package core

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/muesli/termenv"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/clientdb"
)

// Call (recipe) inspection for the LLM builtins: rebuild the dagql ID behind
// a call digest from the frames the client's telemetry store holds, and
// render it in the views cmd/dump-id offers for a saved ID (dagui.RecipeGraph)
// -- plus the content search over every call the TUI console serves as
// /calls (dagui.DB.GrepCalls).
//
// The rebuild is dagui.DB.CallIDForDigest, the same path the TUI takes to
// address an agent from its roster or resume a conversation, fed by a scoped
// load: starting from the digest, each frame's references (receiver, module,
// ID-valued arguments) are followed through the store's call index
// (clientdb.SelectCallFrames), so the cost is linear in the recipe rather
// than the session, and nothing is retained between calls. A frame the
// client never received surfaces as CallIDForDigest's gap report, naming
// the frame that referenced it.

// callViewChain, callViewTree, callViewStats and callViewFind are the values
// of InspectCall's `view` argument.
const (
	callViewChain = "chain"
	callViewTree  = "tree"
	callViewStats = "stats"
	callViewFind  = "find"
)

// findCallsDefaultLimit bounds a FindCalls result when the caller gives no
// limit, matching the console's /calls cap.
const findCallsDefaultLimit = 200

// callInspectFormat is the literal truncation and spine elision the views
// apply -- dump-id's defaults, which have proven readable.
var callInspectFormat = dagui.RecipeFormat{MaxLiteral: 72, Spine: 12}

// callInspectOpts selects and parameterizes an InspectCall view.
type callInspectOpts struct {
	View string
	// Find is the regexp the find view matches against qualified call names.
	Find *regexp.Regexp
	// Depth bounds how far the tree view recurses into ID arguments (0 =
	// unlimited).
	Depth int
	// Diff, when set, structurally diffs the call's recipe against this
	// digest's instead of rendering a view.
	Diff string
}

// inspectCall rebuilds the ID for digest and renders the requested view.
func inspectCall(ctx context.Context, digest string, opts callInspectOpts) (string, error) {
	clientDB, err := traceReportClientDB(ctx)
	if err != nil {
		return "", err
	}
	defer clientDB.Close()
	return inspectCallIn(ctx, clientDB.Read(), digest, opts)
}

// inspectCallIn is inspectCall against an already-open store.
func inspectCallIn(ctx context.Context, read *clientdb.DB, digest string, opts callInspectOpts) (string, error) {
	src, err := loadRecipe(ctx, read, digest)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if opts.Diff != "" {
		other, err := loadRecipe(ctx, read, opts.Diff)
		if err != nil {
			return "", err
		}
		dagui.WriteRecipeDiff(&out, src.RecipeSource, other.RecipeSource, callInspectFormat)
		return out.String(), nil
	}
	switch opts.View {
	case callViewChain, "":
		// The chain as the TUI renders it: every selector on the receiver
		// spine, in the vocabulary the reader already sees in span names.
		term := idtui.NewOutput(&out, termenv.WithProfile(termenv.Ascii))
		if err := new(idtui.Dump).DumpID(term, src.id); err != nil {
			return "", err
		}
	case callViewTree:
		src.Graph.WriteTree(&out, callInspectFormat, opts.Depth)
	case callViewStats:
		src.Graph.WriteStats(&out, src.RecipeSource, callInspectFormat)
	case callViewFind:
		if opts.Find == nil {
			return "", fmt.Errorf("the %s view needs a `find` pattern", callViewFind)
		}
		src.Graph.WriteFind(&out, opts.Find, callInspectFormat)
	default:
		return "", fmt.Errorf("unknown view %q: want %s, %s, %s or %s", opts.View, callViewChain, callViewTree, callViewStats, callViewFind)
	}
	return out.String(), nil
}

// loadedRecipe is a rebuilt ID with the graph the views render.
type loadedRecipe struct {
	dagui.RecipeSource
	id *call.ID
}

// loadRecipe rebuilds the ID for digest from the store and indexes it.
func loadRecipe(ctx context.Context, read *clientdb.DB, digest string) (*loadedRecipe, error) {
	db := dagui.NewDB()
	if err := loadCallClosure(ctx, read, db, digest); err != nil {
		return nil, err
	}
	id, err := db.CallIDForDigest(digest)
	if err != nil {
		return nil, err
	}
	dag, err := id.ToProto()
	if err != nil {
		return nil, err
	}
	recipe := dag.GetRecipe()
	if recipe == nil {
		return nil, fmt.Errorf("call %s is not a recipe", digest)
	}
	encoded, err := id.Encode()
	if err != nil {
		return nil, err
	}
	return &loadedRecipe{
		RecipeSource: dagui.RecipeSource{
			Label:    digest,
			Encoded:  len(encoded),
			RawBytes: rawIDBytes(encoded),
			Graph:    dagui.NewRecipeGraph(recipe),
		},
		id: id,
	}, nil
}

// rawIDBytes is the protobuf size behind a base64 ID -- dump-id reports it
// alongside the base64 length, so keep the header the same.
func rawIDBytes(encoded string) int {
	n := len(encoded) / 4 * 3
	return n - strings.Count(encoded[max(0, len(encoded)-2):], "=")
}

// loadCallClosure feeds db every frame the store holds that is reachable
// from digest: the frame itself, then -- breadth-first, one index lookup per
// batch -- everything each loaded frame references. Frames the store lacks
// are simply not loaded; CallIDForDigest is the one that reports them, with
// the referrer that wanted them.
func loadCallClosure(ctx context.Context, read *clientdb.DB, db *dagui.DB, digest string) error {
	pending := map[string]struct{}{digest: {}}
	visited := map[string]struct{}{}
	for len(pending) > 0 {
		for d := range pending {
			visited[d] = struct{}{}
		}
		frames, err := read.SelectCallFrames(ctx, pending)
		if err != nil {
			return fmt.Errorf("select call frames: %w", err)
		}
		if err := ingestCallFrames(ctx, db, frames); err != nil {
			return err
		}
		next := map[string]struct{}{}
		for d := range pending {
			frame := db.Calls[d]
			if frame == nil {
				continue
			}
			for _, ref := range callReferences(frame) {
				if _, seen := visited[ref]; !seen {
					next[ref] = struct{}{}
				}
			}
		}
		pending = next
	}
	return nil
}

// ingestCallFrames installs frames into db: a payload record decodes straight
// into the call table (it is the frame, digest included), while a
// span-carried frame goes through the span exporter, which decodes the
// legacy dag.call attribute the same way the live TUI does.
func ingestCallFrames(ctx context.Context, db *dagui.DB, frames []clientdb.CallFrame) error {
	var spans []clientdb.Span
	for _, frame := range frames {
		switch {
		case frame.Log != nil:
			decoded, err := clientdb.CallPayloadBody(*frame.Log)
			if err != nil {
				return fmt.Errorf("decode call payload for %s: %w", frame.Digest, err)
			}
			if _, present := db.Calls[frame.Digest]; !present {
				db.Calls[frame.Digest] = decoded
			}
		case frame.Span != nil:
			spans = append(spans, *frame.Span)
		}
	}
	if len(spans) > 0 {
		if err := ingestSpanRows(ctx, db, spans); err != nil {
			return err
		}
	}
	return nil
}

// callReferences lists the digests a frame references: its receiver, its
// module's call, and every ID literal among its arguments (inside lists and
// objects included) -- the same edges CallIDForDigest's extraction follows.
func callReferences(frame *callpbv1.Call) []string {
	var refs []string
	if frame.ReceiverDigest != "" {
		refs = append(refs, frame.ReceiverDigest)
	}
	if frame.Module != nil && frame.Module.CallDigest != "" {
		refs = append(refs, frame.Module.CallDigest)
	}
	for _, arg := range frame.Args {
		refs = appendLiteralReferences(refs, arg.GetValue())
	}
	return refs
}

func appendLiteralReferences(refs []string, lit *callpbv1.Literal) []string {
	switch v := lit.GetValue().(type) {
	case *callpbv1.Literal_CallDigest:
		if v.CallDigest != "" {
			refs = append(refs, v.CallDigest)
		}
	case *callpbv1.Literal_List:
		for _, elem := range v.List.GetValues() {
			refs = appendLiteralReferences(refs, elem)
		}
	case *callpbv1.Literal_Object:
		for _, field := range v.Object.GetValues() {
			refs = appendLiteralReferences(refs, field.GetValue())
		}
	}
	return refs
}

// spanCallDigest resolves a span to the digest of the call it reports on,
// loading just that span.
func spanCallDigest(ctx context.Context, read *clientdb.DB, spanID string) (string, error) {
	if !read.HasSpan(spanID) {
		return "", fmt.Errorf("no span %q in this trace", spanID)
	}
	db := dagui.NewDB()
	if err := ingestSpanScope(ctx, read, db, map[string]struct{}{spanID: {}}); err != nil {
		return "", err
	}
	for _, sp := range db.Spans.Order {
		if sp.Received && sp.ID.String() == spanID {
			if sp.CallDigest == "" {
				return "", fmt.Errorf("span %s is not a dagql call (no call digest)", spanID)
			}
			return sp.CallDigest, nil
		}
	}
	return "", fmt.Errorf("no span %q in this trace", spanID)
}

// findCalls content-searches every call the store holds a frame for; see
// dagui.DB.GrepCalls for the line format.
func findCalls(ctx context.Context, re *regexp.Regexp, limit int) (string, error) {
	clientDB, err := traceReportClientDB(ctx)
	if err != nil {
		return "", err
	}
	defer clientDB.Close()
	return findCallsIn(ctx, clientDB.Read(), re, limit)
}

// findCallsIn is findCalls against an already-open store. The load is
// session-wide -- one row per distinct call, seeded from the call index --
// transient and linear in the number of calls rather than retained.
func findCallsIn(ctx context.Context, read *clientdb.DB, re *regexp.Regexp, limit int) (string, error) {
	digests := read.CallDigests()
	if len(digests) == 0 {
		return "(no calls in this session)", nil
	}
	frames, err := read.SelectCallFrames(ctx, digests)
	if err != nil {
		return "", fmt.Errorf("select call frames: %w", err)
	}
	db := dagui.NewDB()
	for start := 0; start < len(frames); start += traceReportBatchSize {
		if err := ingestCallFrames(ctx, db, frames[start:min(start+traceReportBatchSize, len(frames))]); err != nil {
			return "", err
		}
	}
	lines := db.GrepCalls(re, limit)
	if len(lines) == 0 {
		return fmt.Sprintf("(no calls matching /%s/ among the %d in this session)", re, len(db.Calls)), nil
	}
	return strings.Join(lines, "\n") + "\n", nil
}
