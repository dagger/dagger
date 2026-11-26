package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

type transferTestValue struct {
	Text   string `json:"text"`
	Child  uint64 `json:"child,omitempty"`
	Recipe string `json:"recipe,omitempty"`
	// PartDeps are the result numbers its snapshot part's probe declares as
	// dependencies, which a receiver installing the part takes on.
	PartDeps []uint64 `json:"partDeps,omitempty"`
	// Fields is an optional body, shaped like a module's type definitions,
	// that gives the value a realistic payload size.
	Fields []transferTestField `json:"fields,omitempty"`
	// Parts, when set, are the parts the codec maps the value's payload to
	// and describes, in place of its one pending snapshot part: a record
	// with parts in each state a part can be in.
	Parts   []transferTestPart `json:"parts,omitempty"`
	rev     atomic.Uint64
	release OnReleaseFunc
	links   []PersistedSnapshotRefLink
	// revisionHook runs on each output revision read, in the reader's goroutine.
	revisionHook func()
	// unready makes the output revision read report a held persistence guard.
	unready atomic.Bool
}

// transferTestField is one named field of a transferTestValue's body: a name,
// a description, a type and its arguments.
type transferTestField struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Type        string            `json:"type"`
	Args        []transferTestArg `json:"args,omitempty"`
}

// transferTestPart is one part a transferTestValue's payload maps to: its
// name, its state (pending, completed, absent or metadata) and the kind of
// its value.
type transferTestPart struct {
	Part  string `json:"part"`
	State string `json:"state"`
	Kind  string `json:"kind,omitempty"`
}

// transferTestParts are the parts v's payload maps to, when its value has
// Parts, at v's path; false otherwise.
func transferTestParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, bool) {
	var p transferTestValue
	if err := json.Unmarshal(v.Payload, &p); err != nil || len(p.Parts) == 0 {
		return nil, false
	}
	outputs := make([]CapturedCodecOutput, 0, len(p.Parts))
	for _, part := range p.Parts {
		outputs = append(outputs, CapturedCodecOutput{Address: PersistedPartAddress{OutputPath: v.Path, Part: PartKey(part.Part)}, State: part.State, ValueKind: part.Kind})
	}
	return outputs, true
}

type transferTestArg struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Default string `json:"default,omitempty"`
}

func (*transferTestValue) Type() *ast.Type {
	return &ast.Type{NamedType: "TransferTestValue", NonNull: true}
}
func (v *transferTestValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	raw, err := json.Marshal(v)
	return PersistedObjectEncoding{JSON: raw, SnapshotLinks: cloneSnapshotRefLinks(v.links)}, err
}
func (v *transferTestValue) PersistedOutputRevision() (OutputRevision, error) {
	if v.revisionHook != nil {
		v.revisionHook()
	}
	if v.unready.Load() {
		return 0, fmt.Errorf("%w: test output in use", ErrPersistStateNotReady)
	}
	return OutputRevision(v.rev.Load()), nil
}
func (*transferTestValue) DecodePersistedObject(ctx context.Context, dec *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	value := new(transferTestValue)
	err := json.Unmarshal(raw, value)
	if err == nil && value.Text == "snapshot" {
		value.links, err = dec.SnapshotRoles(ctx)
	}
	if err == nil {
		if hook, ok := transferDecodeHooks.Load(transferDecodeHookKey{dec.Server(), dec.ResultID()}); ok {
			err = hook.(transferDecodeHook)(ctx, dec, value)
		}
	}
	return value, err
}

type transferTestCodec struct{}

func (transferTestCodec) VisitPersistedReferences(v PersistedPayloadVisit, visit PersistedRefVisitor) (json.RawMessage, error) {
	if steps := transferTestCodecSteps.Load(); steps != nil {
		(*steps)("visit", v)
	}
	return transferTestVisit(v, visit)
}

// transferTestVisit visits a transferTestValue payload's references.
func transferTestVisit(v PersistedPayloadVisit, visit PersistedRefVisitor) (json.RawMessage, error) {
	if err := VisitPersistedSnapshotRoles(visit, PersistedRefOutputRole, v.Path, v.SnapshotLinks); err != nil {
		return nil, err
	}
	value := new(transferTestValue)
	if err := json.Unmarshal(v.Payload, value); err != nil {
		return nil, err
	}
	if value.Child != 0 {
		if _, err := VisitPersistedRow(visit, PersistedRefChild, v.Path.Field("child"), &value.Child); err != nil {
			return nil, err
		}
	}
	if value.Recipe != "" {
		if _, err := VisitPersistedCallID(visit, PersistedRefChild, v.Path.Field("recipe"), &value.Recipe); err != nil {
			return nil, err
		}
	}
	return json.Marshal(value)
}
func (transferTestCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: slices.Clone(v.Payload)}, nil
}

// transferTestCodecSteps, when set, sees each payload visit the codec's
// ValidateForeign, MapSnapshotParts and VisitPersistedReferences get, by
// step: validate, map and visit.
var transferTestCodecSteps atomic.Pointer[func(step string, v PersistedPayloadVisit)]

func (transferTestCodec) ValidateForeign(v PersistedPayloadVisit) error {
	if steps := transferTestCodecSteps.Load(); steps != nil {
		(*steps)("validate", v)
	}
	_, err := transferTestVisit(v, func(*PersistedRef) error { return nil })
	return err
}

// transferTestMapped counts the payloads the codec has mapped to their parts,
// by each payload's text: a test whose values have a text of their own counts
// how often the cache decodes its records, beside other tests in parallel.
var transferTestMapped sync.Map // text → *atomic.Int64

// transferTestMappedCount is how many payloads of the text the codec has
// mapped to their parts.
func transferTestMappedCount(text string) int64 {
	n, _ := transferTestMapped.LoadOrStore(text, new(atomic.Int64))
	return n.(*atomic.Int64).Load()
}

func (transferTestCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	if steps := transferTestCodecSteps.Load(); steps != nil {
		(*steps)("map", v)
	}
	var mapped struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(v.Payload, &mapped) == nil {
		n, _ := transferTestMapped.LoadOrStore(mapped.Text, new(atomic.Int64))
		n.(*atomic.Int64).Add(1)
	}
	if outputs, ok := transferTestParts(v); ok {
		return outputs, nil
	}
	return []CapturedCodecOutput{{Address: PersistedPartAddress{OutputPath: v.Path, Part: "snapshot"}, State: "pending", Value: &SnapshotValue{Kind: "directory"}}}, nil
}
func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.Transfer", Typed: (*transferTestValue)(nil), Visitor: transferTestCodec{}, Transfer: transferTestCodec{}})
}
func transferTestCache(t testing.TB) (context.Context, *Cache, *Server) {
	t.Helper()
	ctx, c, srv := persistedListTestCache(t, filepath.Join(t.TempDir(), "cache.db"))
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	return ctx, c, srv
}
func exportTestBundle(t testing.TB, ctx context.Context, c *Cache, roots ...AnyResult) ValueBundle {
	t.Helper()
	var bundle ValueBundle
	require.NoError(t, c.WithExportedValues(ctx, ValueSelection{Roots: roots}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error { bundle = values.Bundle; return nil }))
	return bundle
}

// cloudTestBundle exports roots from c and sends each offer on a row of the
// bundle as the Cloud sends a part it stores (StoredBundle): as that row's
// output, with its owner, and the rows its owner and its Services name in the
// bundle, beside the roots' closure rather than as roots. An engine's export
// carries no offers, so a test of what an engine does with an offer it is
// sent builds its bundle here. An expired row an owner names, which an export
// refuses as a root, is exported unexpired and goes with its expiry.
func cloudTestBundle(t *testing.T, ctx context.Context, c *Cache, roots ...AnyResult) ValueBundle {
	t.Helper()
	sent := slices.Clone(roots)
	lifted := map[*sharedResult]int64{}
	defer func() {
		c.egraphMu.Lock()
		for row, expiry := range lifted {
			row.expiresAtUnix = expiry
		}
		c.egraphMu.Unlock()
	}()
	for {
		bundle := exportTestBundle(t, ctx, c, sent...)
		ordinals := map[uint64]uint64{}
		for _, value := range bundle.Values {
			ordinals[value.SenderNumber] = uint64(value.Ordinal)
		}
		var offers []TransferredOutput
		var missing []AnyResult
		named := map[uint64]bool{}
		relocate := func(id *uint64) {
			if ordinal, ok := ordinals[*id]; ok {
				*id = ordinal
				return
			}
			if named[*id] {
				return
			}
			named[*id] = true
			c.egraphMu.Lock()
			row := c.resultsByID[sharedResultID(*id)]
			if row != nil && row.expiresAtUnix != 0 && row.expiresAtUnix <= time.Now().Unix() {
				lifted[row] = row.expiresAtUnix
				row.expiresAtUnix = 0
			}
			c.egraphMu.Unlock()
			require.NotNil(t, row, "an offer names row %d", *id)
			missing = append(missing, Result[Typed]{shared: row})
		}
		for _, value := range bundle.Values {
			c.egraphMu.RLock()
			held := c.resultsByID[sharedResultID(value.SenderNumber)].testPartOffers()
			c.egraphMu.RUnlock()
			for _, slot := range held {
				copied, err := clonePartOffers([]PersistedPartOffer{slot.record()})
				require.NoError(t, err)
				offer := copied[0]
				for i := range offer.Owner.DependencyIDs {
					relocate(&offer.Owner.DependencyIDs[i])
				}
				for i := range offer.Value.Services {
					relocate(&offer.Value.Services[i].ServiceResultID)
				}
				offers = append(offers, TransferredOutput{Ordinal: value.Ordinal, Address: offer.Address, State: "completed", Value: &offer.Value, Chain: &offer.Chain, Owner: &offer.Owner})
			}
		}
		if len(missing) > 0 {
			sent = append(sent, missing...)
			continue
		}
		bundle.Roots = bundle.Roots[:len(roots)]
		for row, expiry := range lifted {
			for i := range bundle.Values {
				if bundle.Values[i].SenderNumber == uint64(row.id) {
					bundle.Values[i].ExpiresAtUnix = expiry
				}
			}
		}
		for _, offer := range offers {
			key, err := partAddressKey(offer.Address)
			require.NoError(t, err)
			bundle.Outputs = slices.DeleteFunc(bundle.Outputs, func(output TransferredOutput) bool {
				other, _ := partAddressKey(output.Address)
				return output.Ordinal == offer.Ordinal && other == key
			})
			bundle.Outputs = append(bundle.Outputs, offer)
		}
		return bundle
	}
}
func transferTestDependency(c *Cache, ctx context.Context, parent, child AnyResult) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	p, d := parent.cacheSharedResult(), child.cacheSharedResult()
	if !p.deps.add(d.id) {
		return
	}
	p.dependencyOwnershipRevision++
	c.rememberDependencyEdgeLocked(p, d)
	c.incrementIncomingOwnershipLocked(ctx, d)
}
func transferTestOffer(t *testing.T, c *Cache, ctx context.Context, parent, child AnyResult) {
	t.Helper()
	c.egraphMu.Lock()
	record := PersistedPartOffer{Address: PersistedPartAddress{Part: "snapshot"}, Value: SnapshotValue{Kind: "directory"}, Owner: PersistedOfferOwner{DependencyIDs: []uint64{uint64(child.cacheSharedResult().id)}}}
	owner, err := c.newOfferOwnerLocked(ctx, record.Owner)
	if err == nil {
		err = c.testAttachPartOfferLocked(parent.cacheSharedResult(), record.Address, &partOffer{layeredOffer: c.storedLayers.hold(record), owner: owner})
	}
	c.egraphMu.Unlock()
	require.NoError(t, err)
}

// A capture reports where its time went: the wait for the graph lock, the
// walk under it, and the records' copy.
func TestValueTransferCapturePhases(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	root := persistedListTestResult(t, ctx, c, srv, "root", &transferTestValue{Text: "phases"})
	var phases ExportPhases
	require.NoError(t, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		phases = values.Phases
		return nil
	}))
	require.GreaterOrEqual(t, phases.WalkWait, time.Duration(0))
	require.Positive(t, phases.Walk)
	require.Positive(t, phases.Copy)
}

// An offer can be left on a part its value already has: a part can complete
// without its gate settling it, and only a settlement retires the offer on
// it. Such an offer is obsolete, and boot retires it. A capture copies no
// offers, so it captures the value at once and leaves the offer as it is.
func TestValueTransferCaptureTakesAValueWithAnOfferOnACompletedOutput(t *testing.T) {
	ctx, c, srv := statePartCache(t, "")
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	value := &statePartValue{}
	root := persistedListTestResult(t, ctx, c, srv, "root", value)
	res := root.cacheSharedResult()
	out, err := c.testOfferParts(ctx, root, []PersistedPartOffer{testLiveOffer()})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	// The part completes, and the gate never settles it.
	value.done.Store(true)
	value.rev.Add(1)
	owners := res.incomingOwnershipCount.Load()

	var bundle ValueBundle
	require.NoError(t, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	require.Len(t, bundle.Values, 1)
	require.Empty(t, bundle.Values[0].Record.Envelope.PendingOffers)
	c.egraphMu.RLock()
	offers := len(res.testPartOffers())
	c.egraphMu.RUnlock()
	require.Equal(t, 1, offers, "the capture leaves the offer for boot to retire")
	require.Equal(t, owners, res.incomingOwnershipCount.Load())
	require.Zero(t, c.activeGlobalOperations.Load())
}

func TestValueTransferCapture(t *testing.T) {
	testValueTransferCaptureConcurrent(t)
	t.Run("direct and offer diamond", func(t *testing.T) {
		ctx, c, srv := transferTestCache(t)
		leaf := persistedListTestResult(t, ctx, c, srv, "leaf", String("shared"))
		left := persistedListTestResult(t, ctx, c, srv, "left", &transferTestValue{Text: "left"})
		right := persistedListTestResult(t, ctx, c, srv, "right", &transferTestValue{Text: "right"})
		root := persistedListTestResult(t, ctx, c, srv, "root", &transferTestValue{Text: "root"})
		transferTestDependency(c, ctx, left, leaf)
		transferTestDependency(c, ctx, right, leaf)
		transferTestDependency(c, ctx, root, left)
		transferTestOffer(t, c, ctx, root, right)
		bundle := exportTestBundle(t, ctx, c, root)
		// The root's record carries no offers, so the row only its offer's
		// owner reaches doesn't go.
		require.Equal(t, map[uint64]bool{rowNumber(root): true, rowNumber(left): true, rowNumber(leaf): true}, senderNumbers(bundle))
		rootValue := bundle.Values[int(bundle.Roots[0].Ordinal)-1]
		require.Len(t, rootValue.DependencyIDs, 1)
		require.Empty(t, rootValue.Record.Envelope.PendingOffers)
		require.Zero(t, c.activeGlobalOperations.Load())
	})
	for _, kind := range []string{"output", "payload", "transfer", "ownership", "requirements", "frame", "busy"} {
		t.Run(kind, func(t *testing.T) {
			ctx, c, srv := transferTestCache(t)
			object := &transferTestValue{Text: "pending"}
			root := persistedListTestResult(t, ctx, c, srv, "root", object)
			res := root.cacheSharedResult()
			owners := res.incomingOwnershipCount.Load()
			c.testTransferCopied = func(uint64) {
				switch kind {
				case "output":
					object.rev.Add(1)
				case "payload":
					res.payloadMu.Lock()
					res.payloadRevision++
					res.payloadMu.Unlock()
				case "transfer":
					c.egraphMu.Lock()
					res.transferRevision++
					c.egraphMu.Unlock()
				case "ownership":
					c.egraphMu.Lock()
					res.dependencyOwnershipRevision++
					c.egraphMu.Unlock()
				case "requirements":
					res.requiredSessionResourcesGen.Add(1)
				case "frame":
					res.storeResultCall(res.loadResultCall().clone())
				case "busy": // Attachment is rechecked after every copy guard is released.
					c.egraphMu.Lock()
					res.openAttachment(nil)
					c.egraphMu.Unlock()
				}
			}
			err := c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}}, config.RefConfig{}, func(context.Context, *ExportedValues) error { t.Fatal("incoherent capture delivered"); return nil })
			c.testTransferCopied = nil
			if kind == "busy" {
				res.endAttachment(nil)
			}
			require.ErrorIs(t, err, ErrPersistStateNotReady)
			require.Equal(t, owners, res.incomingOwnershipCount.Load())
			require.Zero(t, c.activeGlobalOperations.Load())
		})
	}
	t.Run("cancellation", func(t *testing.T) {
		ctx, c, srv := transferTestCache(t)
		root := persistedListTestResult(t, ctx, c, srv, "cancel-root", String("value"))
		ctx, cancel := context.WithCancel(ctx)
		c.testTransferCopied = func(uint64) { cancel() }
		err := c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}}, config.RefConfig{}, func(context.Context, *ExportedValues) error { t.Fatal("canceled capture delivered"); return nil })
		require.ErrorIs(t, err, context.Canceled)
		require.Zero(t, c.activeGlobalOperations.Load())
	})
}

func TestValueTransferReferences(t *testing.T) {
	ctx, a, srv := transferTestCache(t)
	value := persistedListTestResult(t, ctx, a, srv, "large-int", Int(9007199254740993))
	list := persistedListTestResult(t, ctx, a, srv, "list", DynamicResultArrayOutput{Elem: Int(0), Values: []AnyResult{value}})
	bundle := exportTestBundle(t, ctx, a, list)
	bctx, b, bsrv := transferTestCache(t)
	for i := range 7 {
		persistedListTestResult(t, bctx, b, bsrv, "padding-"+string(rune('a'+i)), String("unrelated"))
	}
	mappingReply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	mapping := mappingReply.Imported()
	require.NoError(t, err)
	require.Len(t, mapping, 1)
	require.Greater(t, mapping[0].ResultID, uint64(7))
	imported, err := b.LoadResultByResultID(bctx, "test-session", bsrv, mapping[0].ResultID)
	require.NoError(t, err)
	require.True(t, IsImportedResult(imported))
	array := imported.Unwrap().(Enumerable)
	item, err := array.NthValue(1, nil)
	require.NoError(t, err)
	require.Equal(t, Int(9007199254740993), item.Unwrap())
	require.NotEqual(t, value.cacheSharedResult().id, item.cacheSharedResult().id)
	forward := exportTestBundle(t, bctx, b, imported)
	require.Equal(t, bundle.Values[0].Record.Envelope.ScalarJSON, forward.Values[0].Record.Envelope.ScalarJSON)
	for _, bad := range []string{"dangling", "cycle", "root", "body", "unreachable"} {
		t.Run(bad, func(t *testing.T) {
			raw, err := json.Marshal(bundle)
			require.NoError(t, err)
			var broken ValueBundle
			require.NoError(t, json.Unmarshal(raw, &broken))
			switch bad {
			case "dangling":
				broken.Values[1].DependencyIDs = []uint64{99}
			case "cycle":
				broken.Values[0].DependencyIDs = []uint64{2}
			case "root":
				broken.Roots[0].Ordinal = 999
			case "body":
				broken.Values[0].Record.Envelope.Items = []PersistedResultEnvelope{{}}
			case "unreachable":
				broken.Roots[0].Ordinal = 1
			}
			before := len(b.resultsByID)
			_, err = b.MergeValues(bctx, cloudCacheID, broken)
			require.Error(t, err)
			require.Len(t, b.resultsByID, before)
		})
	}
	// An extra digest on a frame merges with it, and classes the entry it
	// arrives with.
	t.Run("extra", func(t *testing.T) {
		raw, err := json.Marshal(bundle)
		require.NoError(t, err)
		var withExtra ValueBundle
		require.NoError(t, json.Unmarshal(raw, &withExtra))
		extra := digest.FromString("carried")
		withExtra.Values[0].Record.Call.ExtraDigests = []call.ExtraDigest{{Digest: extra, Label: call.ExtraDigestLabelContent}}
		cctx, c, _ := transferTestCache(t)
		_, err = c.MergeValues(cctx, cloudCacheID, withExtra)
		require.NoError(t, err)
		c.egraphMu.RLock()
		class, known := c.egraphDigestToClass[extra.String()]
		results := c.outputEqClassResults[c.eqClassRootLocked(class)].Size()
		c.egraphMu.RUnlock()
		require.True(t, known)
		require.Equal(t, 1, results)
	})
}

func TestValueTransferImportPublication(t *testing.T) {
	testValueTransferImportConcurrent(t)
	ctx, a, srv := transferTestCache(t)
	leaf := persistedListTestResult(t, ctx, a, srv, "leaf", String("value"))
	root := persistedListTestResult(t, ctx, a, srv, "root", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{leaf}})
	bundle := exportTestBundle(t, ctx, a, root)
	t.Run("preparation failure", func(t *testing.T) {
		ctx, b, _ := transferTestCache(t)
		injected := errors.New("later identity plan refused")
		b.testTransferPlanPrepared = func(n int) error {
			if n == 2 {
				return injected
			}
			return nil
		}
		_, err := b.MergeValues(ctx, cloudCacheID, bundle)
		require.ErrorIs(t, err, injected)
		require.Empty(t, b.resultsByID)
		require.Empty(t, b.persistedEdgesByResult)
		require.Empty(t, b.eqClassToDigests)
	})
	for _, committed := range []bool{false, true} {
		name := "before commit"
		if committed {
			name = "after commit"
		}
		t.Run(name, func(t *testing.T) {
			ctx, b, _ := transferTestCache(t)
			ctx, cancel := context.WithCancel(ctx)
			if committed {
				b.testAfterTransferCommit = cancel
			} else {
				b.testBeforeTransferCommit = cancel
			}
			mappingReply, err := b.MergeValues(ctx, cloudCacheID, bundle)
			mapping := mappingReply.Imported()
			if committed {
				require.NoError(t, err)
				require.Len(t, mapping, 1)
				require.Len(t, b.resultsByID, 2)
			} else {
				require.ErrorIs(t, err, context.Canceled)
				require.Empty(t, b.resultsByID)
			}
		})
	}
	t.Run("finite root expiry", func(t *testing.T) {
		ctx, b, _ := transferTestCache(t)
		expiry := time.Now().Add(time.Hour).Unix()
		bundle.Roots[0].ExpiresAtUnix = expiry
		mappingReply, err := b.MergeValues(ctx, cloudCacheID, bundle)
		mapping := mappingReply.Imported()
		require.NoError(t, err)
		require.Equal(t, expiry, b.persistedEdgesByResult[sharedResultID(mapping[0].ResultID)].expiresAtUnix)
	})
}

// A recipe ID inside a payload travels with every extra digest of every
// vertex: the receiver's content digest as well as the call's. The export
// copies the DAG, so the original is untouched, and the receiving cache
// accepts the recipe as it is.
func TestValueTransferReferencesExtras(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	sourceContent, childContent := digest.FromString("source-content"), digest.FromString("child-content")
	recipe := call.New().Append(String("").Type(), "source").With(call.WithContentDigest(sourceContent)).Append(String("").Type(), "child").With(call.WithContentDigest(childContent))
	raw, err := recipe.Encode()
	require.NoError(t, err)
	value := persistedListTestResult(t, ctx, c, srv, "recipe", &transferTestValue{Text: "recipe", Recipe: raw})
	bundle := exportTestBundle(t, ctx, c, value)
	var payload transferTestValue
	require.NoError(t, json.Unmarshal(bundle.Values[0].Record.Envelope.ObjectJSON, &payload))
	var copied call.ID
	require.NoError(t, copied.Decode(payload.Recipe))
	require.Equal(t, childContent, copied.ContentDigest())
	require.Equal(t, sourceContent, copied.Receiver().ContentDigest(), "the receiver's content digest travels too")
	require.Equal(t, recipe.Receiver().ExtraDigests(), copied.Receiver().ExtraDigests())
	require.NotSame(t, recipe.Receiver(), copied.Receiver(), "the export copies the DAG")
	filtered, err := recipe.FilterTransferDigests()
	require.NoError(t, err)
	require.Equal(t, filtered.Digest(), copied.Digest())
	bctx, b, _ := transferTestCache(t)
	_, err = b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	// The original recipe, as the sender holds it, is accepted as it is.
	bundle.Values[0].Record.Envelope.ObjectJSON = json.RawMessage(`{"text":"original","recipe":` + string(mustTransferJSON(t, raw)) + `}`)
	_, err = b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
}
func mustTransferJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return raw
}

// transferDecodeHooks holds what a test runs when a transferTestValue
// decodes, by the server it decodes for and the row it decodes. Every cache
// numbers its rows from 1, so parallel tests share row ids; each test has a
// server of its own.
var transferDecodeHooks sync.Map

type transferDecodeHookKey struct {
	server   *Server
	resultID uint64
}
type transferDecodeHook = func(context.Context, *PersistDecodeContext, *transferTestValue) error

// onTransferDecode runs hook when row resultID decodes for srv, until the
// test ends.
func onTransferDecode(t testing.TB, srv *Server, resultID uint64, hook transferDecodeHook) {
	t.Helper()
	key := transferDecodeHookKey{srv, resultID}
	transferDecodeHooks.Store(key, hook)
	t.Cleanup(func() { transferDecodeHooks.Delete(key) })
}

func (v *transferTestValue) OnRelease(ctx context.Context) error {
	if v.release != nil {
		return v.release(ctx)
	}
	return nil
}

func TestValueTransferPersistenceDecodePublication(t *testing.T) {
	ctx, a, srv := transferTestCache(t)
	source := persistedListTestResult(t, ctx, a, srv, "encoded-source", &transferTestValue{Text: "old"})
	bundle := exportTestBundle(t, ctx, a, source)
	ctx, b, srv := transferTestCache(t)
	mappingReply, err := b.MergeValues(ctx, cloudCacheID, bundle)
	mapping := mappingReply.Imported()
	require.NoError(t, err)
	id := mapping[0].ResultID
	row := b.resultsByID[sharedResultID(id)]
	var rowCleanups, losingReleases, winningReleases atomic.Int32
	row.onRelease = joinOnRelease(row.onRelease, func(context.Context) error { rowCleanups.Add(1); return nil })
	row.payloadMu.Lock()
	row.snapshotState().ownerLinks = []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "old-applied"}}
	row.snapshotState().linkIntent = &snapshotLinkIntent{Links: []PersistedSnapshotRefLink{}}
	row.payloadRevision++
	row.payloadMu.Unlock()
	var calls int
	onTransferDecode(t, srv, id, func(ctx context.Context, dec *PersistDecodeContext, value *transferTestValue) error {
		calls++
		roles, err := dec.SnapshotRoles(ctx)
		require.NoError(t, err)
		if calls == 1 {
			require.Empty(t, roles, "an empty copied map is authoritative")
			value.release = func(context.Context) error { losingReleases.Add(1); return nil }
			row.payloadMu.Lock()
			next, err := clonePersistedEnvelope(*row.persistedEnvelope)
			if err != nil {
				row.payloadMu.Unlock()
				return err
			}
			next.ObjectJSON = json.RawMessage(`{"text":"new"}`)
			row.persistedEnvelope = &next
			row.snapshotState().linkIntent = &snapshotLinkIntent{Links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "new-desired"}}}
			row.payloadRevision++
			row.payloadMu.Unlock()
		} else {
			require.Equal(t, []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "new-desired"}}, roles)
			value.release = func(context.Context) error { winningReleases.Add(1); return nil }
		}
		return nil
	})
	loaded, err := b.LoadResultByResultID(ctx, "test-session", srv, id)
	require.NoError(t, err)
	require.Equal(t, "new", loaded.Unwrap().(*transferTestValue).Text)
	require.Equal(t, 2, calls)
	require.EqualValues(t, 1, losingReleases.Load())
	require.Zero(t, rowCleanups.Load())
	require.NoError(t, b.ReleaseSession(ctx, "test-session"))
	_, removed, err := b.removePersistedEdge(ctx, row.id)
	require.NoError(t, err)
	require.True(t, removed)
	require.EqualValues(t, 1, rowCleanups.Load())
	require.EqualValues(t, 1, winningReleases.Load())
}

func (v *transferTestValue) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink {
	return cloneSnapshotRefLinks(v.links)
}

// ResolveChainPrefixes gives a prefix output the first layers of the chain it
// names, which it shares, and refuses a prefix that names no output carrying a
// chain, more layers than that chain has, or none.
func TestResolveChainPrefixes(t *testing.T) {
	t.Parallel()
	fs := PersistedPartAddress{Part: "fs"}
	layer := func(s string) snapshots.ExportLayer {
		return snapshots.ExportLayer{Descriptor: ocispecs.Descriptor{Digest: digest.FromString(s), Size: 1}}
	}
	chain := []snapshots.ExportLayer{layer("a"), layer("b"), layer("c")}
	bundle := func(prefix ChainPrefix, extra ...TransferredOutput) *ValueBundle {
		return &ValueBundle{Outputs: append([]TransferredOutput{
			{Ordinal: 3, Address: fs, State: "completed", Chain: &OfferedChain{Layers: chain}},
			{Ordinal: 2, Address: fs, State: "completed", Prefix: &prefix},
		}, extra...)}
	}

	b := bundle(ChainPrefix{Ordinal: 3, Address: fs, Layers: 2})
	require.NoError(t, ResolveChainPrefixes(b))
	require.Nil(t, b.Outputs[1].Prefix)
	require.Equal(t, chain[:2], b.Outputs[1].Chain.Layers)
	require.Equal(t, 2, cap(b.Outputs[1].Chain.Layers), "an append to the prefix's layers can't reach its chain's")
	require.Same(t, &chain[0], &b.Outputs[1].Chain.Layers[0], "the layers are the chain's own")

	// The prefix reads its blobs where the chain does: the chain's addresses,
	// packs and renewal key for the prefix's layers, and none of the rest.
	addressed := bundle(ChainPrefix{Ordinal: 3, Address: fs, Layers: 2})
	pack := digest.FromString("pack")
	addressed.Outputs[0].Chain = &OfferedChain{
		Layers:     chain,
		Addresses:  map[digest.Digest]BlobAddress{chain[0].Descriptor.Digest: {URL: "dagger-image:" + string(chain[0].Descriptor.Digest)}, chain[2].Descriptor.Digest: {URL: "dagger-image:" + string(chain[2].Descriptor.Digest)}},
		RenewalKey: "renew",
		Packs:      map[digest.Digest]digest.Digest{chain[1].Descriptor.Digest: pack, chain[2].Descriptor.Digest: pack},
	}
	require.NoError(t, ResolveChainPrefixes(addressed))
	got := addressed.Outputs[1].Chain
	require.Equal(t, map[digest.Digest]BlobAddress{chain[0].Descriptor.Digest: {URL: "dagger-image:" + string(chain[0].Descriptor.Digest)}}, got.Addresses)
	require.Equal(t, map[digest.Digest]digest.Digest{chain[1].Descriptor.Digest: pack}, got.Packs)
	require.Equal(t, "renew", got.RenewalKey)

	for _, tc := range []struct {
		name   string
		bundle *ValueBundle
	}{
		{"no layers", bundle(ChainPrefix{Ordinal: 3, Address: fs, Layers: 0})},
		{"more layers than the chain", bundle(ChainPrefix{Ordinal: 3, Address: fs, Layers: 4})},
		{"no such output", bundle(ChainPrefix{Ordinal: 9, Address: fs, Layers: 1})},
		{"another part of the output", bundle(ChainPrefix{Ordinal: 3, Address: PersistedPartAddress{Part: "other"}, Layers: 1})},
		{"an output that is itself a prefix", bundle(ChainPrefix{Ordinal: 2, Address: fs, Layers: 1})},
		{"a chain and a prefix both", bundle(ChainPrefix{Ordinal: 3, Address: fs, Layers: 1}, TransferredOutput{Ordinal: 4, Address: fs, State: "completed", Chain: &OfferedChain{Layers: chain}, Prefix: &ChainPrefix{Ordinal: 3, Address: fs, Layers: 1}})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, ResolveChainPrefixes(tc.bundle))
		})
	}
}

// A prefix candidate that delegates to a parent part the bundle offers gets
// no prefix output; one that delegates to a part the bundle doesn't offer, or
// doesn't delegate, gets one.
func TestPrefixServedByDelegation(t *testing.T) {
	t.Parallel()
	rows := map[sharedResultID]*capturedTransferRow{7: {ordinal: 2}}
	fs, err := partAddressKey(PersistedPartAddress{Part: "fs"})
	require.NoError(t, err)
	delegating := LazyOperationRoute{Delegation: &PartDelegation{ParentResultID: 7, Address: PersistedPartAddress{Part: "fs"}}}
	for _, tc := range []struct {
		name    string
		route   LazyOperationRoute
		offered map[string]bool
		want    bool
	}{
		{"delegating, the parent part offered", delegating, map[string]bool{"2:" + fs: true}, true},
		{"delegating, the parent part not offered", delegating, map[string]bool{}, false},
		{"delegating, another part of the parent offered", delegating, map[string]bool{"2:other": true}, false},
		{"delegating to a parent outside the capture", LazyOperationRoute{Delegation: &PartDelegation{ParentResultID: 9, Address: PersistedPartAddress{Part: "fs"}}}, map[string]bool{"2:" + fs: true}, false},
		{"not delegating", LazyOperationRoute{}, map[string]bool{"2:" + fs: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, prefixServedByDelegation(tc.route, rows, tc.offered))
		})
	}
}

// A selected output whose entry the captured closure doesn't hold is refused,
// unless the selection asks to leave such outputs out: then it is left out,
// and the rest of the bundle is exported.
func TestExportLeavesOutOutputsOutsideTheClosureOnRequest(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	inside := persistedListTestResult(t, ctx, c, srv, "inside", &transferTestValue{Text: "pending"})
	outside := persistedListTestResult(t, ctx, c, srv, "outside", &transferTestValue{Text: "pending"})
	selection := ValueSelection{
		Roots: []AnyResult{inside},
		Outputs: []SelectedValueOutput{
			{Result: inside, Address: PersistedPartAddress{Part: "snapshot"}},
			{Result: outside, Address: PersistedPartAddress{Part: "snapshot"}},
		},
	}
	consume := func(context.Context, *ExportedValues) error { return nil }
	require.ErrorContains(t, c.WithExportedValues(ctx, selection, config.RefConfig{}, consume), "outside captured closure")

	selection.LeaveOutOutputsOutsideClosure = true
	var bundle ValueBundle
	require.NoError(t, c.WithExportedValues(ctx, selection, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	require.Len(t, bundle.Values, 1)
	require.Len(t, bundle.Outputs, 1, "only the output inside the closure")
	require.Equal(t, bundle.Values[0].Ordinal, bundle.Outputs[0].Ordinal)
}

func testValueTransferCaptureConcurrent(t *testing.T) {
	t.Run("concurrent replacement and pruning", func(t *testing.T) {
		ctx, c, srv := transferTestCache(t)
		root := persistedListTestResult(t, ctx, c, srv, "root", &transferTestValue{Text: "pending"})
		old := persistedListTestResult(t, ctx, c, srv, "old", String("old"))
		next := persistedListTestResult(t, ctx, c, srv, "next", String("next"))
		transferTestOffer(t, c, ctx, root, old)
		entered, finish := make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}}, config.RefConfig{}, func(context.Context, *ExportedValues) error { close(entered); <-finish; return nil })
		}()
		<-entered
		c.egraphMu.Lock()
		owner, err := c.newOfferOwnerLocked(ctx, PersistedOfferOwner{DependencyIDs: []uint64{uint64(next.cacheSharedResult().id)}})
		var releases []OnReleaseFunc
		if err == nil {
			address := PersistedPartAddress{Part: "snapshot"}
			var queue []*sharedResult
			queue, err = c.testReplacePartOfferLocked(ctx, root.cacheSharedResult(), address, &partOffer{owner: owner, layeredOffer: layeredOffer{held: PersistedPartOffer{Address: address, Owner: owner.record, Value: SnapshotValue{Kind: "directory"}}}})
			if err == nil {
				releases, err = c.collectUnownedResultsLocked(ctx, queue)
			}
		}
		c.egraphMu.Unlock()
		require.NoError(t, err)
		require.NoError(t, runOnReleaseFuncs(ctx, releases))
		for _, row := range []AnyResult{root, old, next} {
			_, _, err := c.removePersistedEdge(ctx, row.cacheSharedResult().id)
			require.NoError(t, err)
		}
		require.NoError(t, c.ReleaseSession(ctx, "test-session"))
		// The capture holds no offer: the replaced offer's owner goes with the
		// replacement, and the row only it owned with it.
		snapshot := c.snapshotPruneState(pruneSnapshotMetadata, 10)
		require.NotContains(t, pruneActiveClosure(snapshot, nil), old.cacheSharedResult().id)
		c.egraphMu.RLock()
		ownerCount := len(c.offerOwners)
		c.egraphMu.RUnlock()
		require.Equal(t, 1, ownerCount)
		close(finish)
		require.NoError(t, <-done)
		require.Empty(t, c.resultsByID)
		require.Empty(t, c.offerOwners)
	})
}

func testValueTransferImportConcurrent(t *testing.T) {
	t.Run("lookup at commit boundary", func(t *testing.T) {
		ctx, a, srv := transferTestCache(t)
		leaf := persistedListTestResult(t, ctx, a, srv, "leaf", String("value"))
		root := persistedListTestResult(t, ctx, a, srv, "root", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{leaf}})
		bundle := exportTestBundle(t, ctx, a, root)
		ctx, b, bsrv := transferTestCache(t)
		entered, commit := make(chan struct{}), make(chan struct{})
		b.testBeforeTransferCommit = func() { close(entered); <-commit }
		done := make(chan error, 1)
		go func() { _, err := b.MergeValues(ctx, cloudCacheID, bundle); done <- err }()
		<-entered
		for _, value := range bundle.Values {
			_, err := b.LoadResultByResultID(ctx, "consumer", bsrv, uint64(value.Ordinal))
			require.ErrorContains(t, err, "missing shared result")
		}
		close(commit)
		require.NoError(t, <-done)
		b.egraphMu.RLock()
		resultCount := len(b.resultsByID)
		b.egraphMu.RUnlock()
		require.Equal(t, len(bundle.Values), resultCount)
		for _, value := range bundle.Values {
			_, err := b.LoadResultByResultID(ctx, "consumer", bsrv, uint64(value.Ordinal))
			require.NoError(t, err)
		}
	})
	t.Run("close waits for admitted import", func(t *testing.T) {
		ctx, a, srv := transferTestCache(t)
		bundle := exportTestBundle(t, ctx, a, persistedListTestResult(t, ctx, a, srv, "root", String("value")))
		ctx, b, _ := transferTestCache(t)
		entered, commit := make(chan struct{}), make(chan struct{})
		b.testBeforeTransferCommit = func() { close(entered); <-commit }
		done := make(chan error, 1)
		go func() { _, err := b.MergeValues(ctx, cloudCacheID, bundle); done <- err }()
		<-entered
		closed := make(chan error, 1)
		go func() { closed <- b.Close(ctx) }()
		select {
		case err := <-closed:
			t.Fatalf("close passed admitted import: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
		close(commit)
		require.NoError(t, <-done)
		require.NoError(t, <-closed)
	})
}

// A capture splits its bundle phase into its steps, summed over its passes:
// holding the known rows, checking the copied records, settling the roots,
// deriving the held rows' recipes, and building the bundle's values. A known
// row whose recipe another row of the closure shares goes in full, which
// takes a second pass.
func TestValueTransferCaptureBundlePhases(t *testing.T) {
	t.Parallel()
	steps := func(p ExportPhases) time.Duration {
		return p.BundleKnown + p.BundleCheck + p.BundleSettle + p.BundleRecipes + p.BundleValues
	}
	t.Run("one pass", func(t *testing.T) {
		t.Parallel()
		ctx, c, srv := transferTestCache(t)
		root := persistedListTestResult(t, ctx, c, srv, "root", &transferTestValue{Text: "bundle phases"})
		var phases ExportPhases
		require.NoError(t, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
			phases = values.Phases
			return nil
		}))
		require.Equal(t, 1, phases.BundlePasses)
		require.Positive(t, phases.BundleCheck, "the root's record is checked")
		require.Positive(t, phases.BundleSettle)
		require.Positive(t, phases.BundleValues)
		require.LessOrEqual(t, steps(phases), phases.Bundle)
	})
	t.Run("a held row carried in full", func(t *testing.T) {
		t.Parallel()
		ctx, c, srv := transferTestCache(t)
		old := persistedListTestResult(t, ctx, c, srv, "phases-recipe", String("old"))
		known := NewKnownRows()
		_, rows, _ := recordsOnlyCapture(t, ctx, c, known, old)
		known.Merged(rows)
		root := persistedListTestResult(t, ctx, c, srv, "phases-recipe-root", String("root"))
		transferTestDependency(c, ctx, root, old)
		currentEntryTestExpire(c, old)
		fresh := persistedListTestResult(t, ctx, c, srv, "phases-recipe", String("fresh"))
		transferTestDependency(c, ctx, root, fresh)

		var phases ExportPhases
		require.NoError(t, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}, Known: known}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
			phases = values.Phases
			return nil
		}))
		require.Equal(t, 2, phases.BundlePasses, "the old entry goes in full in a second pass")
		require.Positive(t, phases.BundleRecipes, "the held row's recipe is derived")
		require.LessOrEqual(t, steps(phases), phases.Bundle)
	})
}
