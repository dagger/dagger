package core

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sort"
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
// ID-valued arguments or implicit inputs) are followed through the store's
// call index (clientdb.SelectCallFrames), so the cost is linear in the recipe
// rather than the session, and nothing is retained between calls. A frame the
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
	// Imported traces are opened only once the live store lacks a frame, so
	// a live recipe never reopens an idle import. A frame the live store
	// holds wins anyway (see ingestCallFrames).
	var imports []*clientdb.DB
	opened := false
	defer func() {
		for _, store := range imports {
			store.Close()
		}
	}()
	for len(pending) > 0 {
		for d := range pending {
			visited[d] = struct{}{}
		}
		missing := func() bool {
			for d := range pending {
				if db.Calls[d] == nil {
					return true
				}
			}
			return false
		}
		load := func(source *clientdb.DB) error {
			frames, err := source.SelectCallFrames(ctx, pending)
			if err != nil {
				return fmt.Errorf("select call frames: %w", err)
			}
			return ingestCallFrames(ctx, db, frames)
		}
		if err := load(read); err != nil {
			return err
		}
		if missing() && !opened && read.HasImports() {
			stores, _, err := read.InspectionStores(ctx)
			if err != nil {
				return err
			}
			imports, opened = stores[1:], true
		}
		for _, source := range imports {
			if !missing() {
				break
			}
			if err := load(source); err != nil {
				return err
			}
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
		// Inspection may combine live and imported captures. A frame already
		// loaded from the live store wins over a historical copy.
		if _, present := db.Calls[frame.Digest]; present {
			continue
		}
		switch {
		case frame.Log != nil:
			decoded, err := clientdb.CallPayloadBody(*frame.Log)
			if err != nil {
				return fmt.Errorf("decode call payload for %s: %w", frame.Digest, err)
			}
			db.Calls[frame.Digest] = decoded
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
// module's call, and every ID literal among its arguments and implicit inputs
// (inside lists and objects included) -- the same edges CallIDForDigest follows.
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
	for _, input := range frame.ImplicitInputs {
		refs = appendLiteralReferences(refs, input.GetValue())
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
// dagui.DB.GrepCalls for the line format. A non-empty exclude leaves out the
// frames of that agent's own conversation (see ownedConversationCalls).
func findCalls(ctx context.Context, re *regexp.Regexp, limit int, exclude string) (string, error) {
	clientDB, err := traceReportClientDB(ctx)
	if err != nil {
		return "", err
	}
	defer clientDB.Close()
	return findCallsIn(ctx, clientDB.Read(), re, limit, exclude)
}

// findCallsIn is findCalls against an already-open store.
//
// It searches every call of the session and of the traces loaded for
// inspection, but never holds them all: the frames are read and decoded a
// chunk at a time, in digest order (the order the result lists them in),
// keeping only which ones match and, to leave out the caller's own
// conversation, the receiver and owner of each LLM-typed one. A second pass
// renders the lines shown from just their frames. A search over a whole
// session held every frame of it at once before.
func findCallsIn(ctx context.Context, read *clientdb.DB, re *regexp.Regexp, limit int, exclude string) (string, error) {
	stores, release, err := read.InspectionStores(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	digests := callDigestsIn(stores)
	total := 0
	var matched []string
	conversation := map[string]conversationFrame{}
	err = scanCallFrames(ctx, stores, digests, func(dig string, frame *callpbv1.Call) (bool, error) {
		total++
		if exclude != "" && frame.GetType().GetNamedType() == "LLM" {
			conversation[dig] = conversationFrameOf(frame)
		}
		if re.MatchString(dagui.CallSearchText(frame)) {
			matched = append(matched, dig)
		}
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if total == 0 {
		return "(no calls in this session)", nil
	}
	hidden := 0
	if exclude != "" {
		owned := ownedConversationCalls(conversation, exclude)
		shown := matched[:0]
		for _, dig := range matched {
			if _, mine := owned[dig]; mine {
				hidden++
				continue
			}
			shown = append(shown, dig)
		}
		matched = shown
	}
	var note string
	if hidden > 0 {
		note = fmt.Sprintf("(%d matching calls of your own conversation hidden; includeSelf: true lists them)", hidden)
	}
	grep := dagui.NewCallGrep(re, limit)
	added := 0
	err = scanCallFrames(ctx, stores, matched, func(_ string, frame *callpbv1.Call) (bool, error) {
		if grep.Full() {
			return false, nil
		}
		grep.Add(frame)
		added++
		return true, nil
	})
	if err != nil {
		return "", err
	}
	grep.Count(len(matched) - added)
	lines := grep.Lines()
	if len(lines) == 0 {
		empty := fmt.Sprintf("(no calls matching /%s/ among the %d in this session)", re, total)
		if note != "" {
			empty += "\n" + note
		}
		return empty, nil
	}
	if note != "" {
		lines = append(lines, note)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// findCallsChunk is how many call frames a session-wide call search holds
// decoded at once.
const findCallsChunk = 512

// callDigestsIn is every call digest any of stores holds a frame for, in
// order.
func callDigestsIn(stores []*clientdb.DB) []string {
	seen := map[string]struct{}{}
	for _, store := range stores {
		for dig := range store.CallDigests() {
			seen[dig] = struct{}{}
		}
	}
	digests := make([]string, 0, len(seen))
	for dig := range seen {
		digests = append(digests, dig)
	}
	sort.Strings(digests)
	return digests
}

// scanCallFrames decodes the frames of digests, a chunk at a time, and
// calls fn with each one decoded, in the order of digests, until fn returns
// false. Each digest's frame is the one a DB loading every store in order
// would keep: the first store's that decodes (see ingestCallFrames).
func scanCallFrames(ctx context.Context, stores []*clientdb.DB, digests []string, fn func(string, *callpbv1.Call) (bool, error)) error {
	for start := 0; start < len(digests); start += findCallsChunk {
		chunk := digests[start:min(start+findCallsChunk, len(digests))]
		pending := make(map[string]struct{}, len(chunk))
		for _, dig := range chunk {
			pending[dig] = struct{}{}
		}
		db := dagui.NewDB()
		for _, store := range stores {
			frames, err := store.SelectCallFrames(ctx, pending)
			if err != nil {
				return fmt.Errorf("select call frames: %w", err)
			}
			if err := ingestCallFrames(ctx, db, frames); err != nil {
				return err
			}
		}
		for _, dig := range chunk {
			frame := db.Calls[dig]
			if frame == nil {
				continue
			}
			more, err := fn(dig, frame)
			if err != nil || !more {
				return err
			}
		}
	}
	return nil
}

// conversationFrame is what ownedConversationCalls reads of an LLM-typed
// frame.
type conversationFrame struct {
	receiver string
	// stamped marks an ownership stamp (LLM.__withOwnerAgent), naming owner.
	stamped bool
	owner   string
}

func conversationFrameOf(frame *callpbv1.Call) conversationFrame {
	c := conversationFrame{receiver: frame.GetReceiverDigest()}
	if frame.GetField() == "__withOwnerAgent" {
		c.stamped = true
		for _, arg := range frame.GetArgs() {
			if arg.GetName() == "handle" {
				c.owner = arg.GetValue().GetString_()
			}
		}
	}
	return c
}

// ownedConversationCalls returns the digests of the frames recording an
// agent's own conversation: LLM-typed frames whose nearest ownership stamp
// (LLM.__withOwnerAgent, which an agent puts on its committed conversation)
// up the receiver chain names handle. Nearest, because a conversation an
// agent hands on -- a worker's seed -- is re-stamped by its new owner. The
// walk only reads LLM-typed frames, so frames holds just those.
func ownedConversationCalls(frames map[string]conversationFrame, handle string) map[string]struct{} {
	owners := make(map[string]string, len(frames))
	for dig := range frames {
		var chain []string
		owner := ""
		for cur := dig; cur != "" && len(chain) < 1<<16; {
			if known, ok := owners[cur]; ok {
				owner = known
				break
			}
			frame, ok := frames[cur]
			if !ok {
				break
			}
			chain = append(chain, cur)
			if frame.stamped {
				owner = frame.owner
				break
			}
			cur = frame.receiver
		}
		for _, d := range chain {
			owners[d] = owner
		}
	}
	owned := map[string]struct{}{}
	for dig, owner := range owners {
		if owner == handle {
			owned[dig] = struct{}{}
		}
	}
	return owned
}
