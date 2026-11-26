package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// partTestValue is a multi-part transfer value: one payload declaring
// several named parts, each pending or completed with its own snapshot. It
// exists so part tests can exercise several addresses of one receiver
// without a real store or a core type.
type partTestState struct {
	Snapshot string `json:"snapshot,omitempty"`
	Absent   bool   `json:"absent,omitempty"`
}

type partTestValue struct {
	Name  string                   `json:"name"`
	Parts map[string]partTestState `json:"parts,omitempty"`
	// mu guards Parts, links and rev the way a core value's output guard
	// does: readers copy under it, and a prepared store holds it from TryLock
	// to Unlock so its Publish cannot race an encoder.
	mu    sync.RWMutex
	rev   atomic.Uint64
	links []PersistedSnapshotRefLink
}

type partTestEncoded struct {
	Name  string                   `json:"name"`
	Parts map[string]partTestState `json:"parts,omitempty"`
}

func (v *partTestValue) payloadLocked() partTestEncoded {
	parts := make(map[string]partTestState, len(v.Parts))
	for name, state := range v.Parts {
		parts[name] = state
	}
	return partTestEncoded{Name: v.Name, Parts: parts}
}

func newPartTestValue(name string, parts map[string]partTestState) *partTestValue {
	v := &partTestValue{Name: name, Parts: parts}
	for part, state := range parts {
		if state.Snapshot != "" {
			v.links = append(v.links, PersistedSnapshotRefLink{Role: part, RefKey: state.Snapshot})
		}
	}
	slices.SortFunc(v.links, func(a, b PersistedSnapshotRefLink) int {
		if a.Role == b.Role {
			return 0
		}
		if a.Role < b.Role {
			return -1
		}
		return 1
	})
	return v
}

func (*partTestValue) Type() *ast.Type {
	return &ast.Type{NamedType: "PartTestValue", NonNull: true}
}

func (v *partTestValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	raw, err := json.Marshal(v.payloadLocked())
	return PersistedObjectEncoding{JSON: raw, SnapshotLinks: cloneSnapshotRefLinks(v.links)}, err
}

func (v *partTestValue) PersistedOutputRevision() (OutputRevision, error) {
	return OutputRevision(v.rev.Load()), nil
}

// PersistedSnapshotRefLinks is what makes an ordinary publication apply this
// value's owner links, exactly as a completed core value does.
func (v *partTestValue) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return cloneSnapshotRefLinks(v.links)
}

// PreparePartStore is the typed receiver's writer: a closed assignment that
// cannot fail, allocate storage or run cleanup once prepared, with its own
// expected output revision, exactly like the File and Directory stores.
func (v *partTestValue) PreparePartStore(_ context.Context, _ *PersistDecodeContext, _ PersistedRecord, d PartDescriptor, ref snapshots.ImmutableRef) (PreparedPartStore, error) {
	if d.SnapshotID == "" {
		return nil, fmt.Errorf("part test store: donated part %q has no snapshot", d.Address.Part)
	}
	revision, err := v.PersistedOutputRevision()
	if err != nil {
		return nil, err
	}
	return &partTestStore{receiver: v, part: string(d.Address.Part), snapshot: d.SnapshotID, expected: revision, ref: ref}, nil
}

type partTestStore struct {
	receiver *partTestValue
	part     string
	snapshot string
	expected OutputRevision
	ref      snapshots.ImmutableRef
}

func (s *partTestStore) TryLock() bool {
	if !s.receiver.mu.TryLock() {
		return false
	}
	if OutputRevision(s.receiver.rev.Load()) != s.expected {
		s.receiver.mu.Unlock()
		return false
	}
	return true
}

func (s *partTestStore) Unlock() {
	s.receiver.mu.Unlock()
}

func (s *partTestStore) Publish() {
	state := s.receiver.Parts[s.part]
	state.Snapshot = s.snapshot
	s.receiver.Parts[s.part] = state
	replaced := false
	for i := range s.receiver.links {
		if s.receiver.links[i].Role == s.part {
			s.receiver.links[i].RefKey = s.snapshot
			replaced = true
		}
	}
	if !replaced {
		s.receiver.links = append(s.receiver.links, PersistedSnapshotRefLink{Role: s.part, RefKey: s.snapshot})
	}
	s.receiver.rev.Add(1)
}

func (*partTestValue) DecodePersistedObject(ctx context.Context, dec *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	value := new(partTestValue)
	if err := json.Unmarshal(raw, value); err != nil {
		return nil, err
	}
	links, err := dec.SnapshotRoles(ctx)
	if err != nil {
		return nil, err
	}
	value.links = links
	return value, nil
}

type partTestCodec struct{}

func (partTestCodec) VisitPersistedReferences(v PersistedPayloadVisit, visit PersistedRefVisitor) (json.RawMessage, error) {
	if err := VisitPersistedSnapshotRoles(visit, PersistedRefOutputRole, v.Path, v.SnapshotLinks); err != nil {
		return nil, err
	}
	return v.Payload, nil
}

func (partTestCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: slices.Clone(v.Payload)}, nil
}

func (partTestCodec) ValidateForeign(PersistedPayloadVisit) error { return nil }

func partTestPayload(v PersistedPayloadVisit) (*partTestValue, error) {
	value := new(partTestValue)
	if err := json.Unmarshal(v.Payload, value); err != nil {
		return nil, err
	}
	return value, nil
}

func partTestRoleKey(links []PersistedSnapshotRefLink, role string) string {
	for _, link := range links {
		if link.Role == role {
			return link.RefKey
		}
	}
	return ""
}

func (partTestCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	value, err := partTestPayload(v)
	if err != nil {
		return nil, err
	}
	parts := slices.Sorted(maps2Keys(value.Parts))
	out := make([]CapturedCodecOutput, 0, len(parts))
	for _, part := range parts {
		state := value.Parts[part]
		o := CapturedCodecOutput{Address: PersistedPartAddress{OutputPath: v.Path, Part: PartKey(part)}, Role: part, State: "pending", ValueKind: "directory"}
		switch {
		case state.Absent:
			o.State = "absent"
		case state.Snapshot != "":
			o.State = "completed"
			o.SnapshotID = state.Snapshot
			o.Value = &SnapshotValue{Kind: "directory"}
		default:
			o.Value = &SnapshotValue{Kind: "directory"}
		}
		out = append(out, o)
	}
	return out, nil
}

// RouteParts lets an ordinary demand route one of this family's parts: it has
// no metadata part, no delegation and no saved Lazy operation, so a demand
// can only take a completed equivalent.
func (partTestCodec) RouteParts(PersistedPayloadVisit, PartKey) (LazyOperationRoute, error) {
	return LazyOperationRoute{}, nil
}

func (partTestCodec) DescribeParts(v PersistedPayloadVisit) ([]PartProbe, error) {
	outputs, err := (partTestCodec{}).MapSnapshotParts(v)
	if err != nil {
		return nil, err
	}
	probes := make([]PartProbe, 0, len(outputs))
	for _, o := range outputs {
		probes = append(probes, PartProbe{
			Descriptor:    PartDescriptor{Address: o.Address, Value: o.Value, Absent: o.State == "absent", SnapshotID: partTestRoleKey(v.SnapshotLinks, o.Role)},
			LocalComplete: o.State == "completed" || o.State == "absent",
		})
	}
	return probes, nil
}

// PreparePartRecord installs one donated part into the receiver's own
// representation, keeping every other declared part and role unchanged.
func (partTestCodec) PreparePartRecord(receiver, source PersistedRecord, d PartDescriptor, target PersistedPartAddress) (PersistedRecord, error) {
	value := new(partTestValue)
	if err := json.Unmarshal(receiver.Envelope.ObjectJSON, value); err != nil {
		return receiver, err
	}
	part := string(target.Part)
	state, ok := value.Parts[part]
	if !ok {
		return receiver, fmt.Errorf("part test: receiver has no part %q", part)
	}
	if d.SnapshotID == "" {
		return receiver, fmt.Errorf("part test: donated part %q has no snapshot", part)
	}
	state.Snapshot = d.SnapshotID
	value.Parts[part] = state
	raw, err := json.Marshal(value)
	if err != nil {
		return receiver, err
	}
	receiver.Envelope.ObjectJSON = raw
	links := cloneSnapshotRefLinks(receiver.SnapshotLinks)
	replaced := false
	for i := range links {
		if links[i].Role == part {
			links[i].RefKey = d.SnapshotID
			replaced = true
		}
	}
	if !replaced {
		links = append(links, PersistedSnapshotRefLink{Role: part, RefKey: d.SnapshotID})
	}
	slices.SortFunc(links, func(a, b PersistedSnapshotRefLink) int {
		if a.Role == b.Role {
			return 0
		}
		if a.Role < b.Role {
			return -1
		}
		return 1
	})
	receiver.SnapshotLinks = links
	return receiver, nil
}

func maps2Keys(m map[string]partTestState) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{
		Name:     "dagql_test.PartTest",
		Typed:    (*partTestValue)(nil),
		Visitor:  partTestCodec{},
		Transfer: partTestCodec{},
	})
}

// partTestRef is a snapshot reference with no storage behind it: installing
// a part from an equivalent pins, opens and releases references, and never
// reads their bytes.
type partTestRef struct {
	id      string
	manager *partTestManager
}

func (r *partTestRef) ID() string         { return r.id }
func (r *partTestRef) SnapshotID() string { return r.id }
func (r *partTestRef) Release(context.Context) error {
	r.manager.released.Add(1)
	return nil
}
func (r *partTestRef) Size(context.Context) (int64, error) { return 0, nil }
func (r *partTestRef) Mount(context.Context, bool) (snapshots.MountableRef, error) {
	panic("part test: a reference is never mounted")
}
func (r *partTestRef) ExportChain(context.Context, config.RefConfig) (*snapshots.ExportChain, error) {
	panic("part test: a reference is never exported")
}
func (r *partTestRef) ExportFileChain(context.Context, config.RefConfig, string) (*snapshots.ExportChain, error) {
	panic("part test: a reference is never exported")
}

var _ snapshots.ImmutableRef = (*partTestRef)(nil)

// partTestManager answers the pin, accessor and lease calls installing a
// part from an equivalent makes. It needs no mount, no privileges and no real
// store.
type partTestManager struct {
	fakeSnapshotManager
	pins     atomic.Int32
	released atomic.Int32
	afterPin func()
}

func (m *partTestManager) PinSnapshot(_ context.Context, id string) (snapshots.ImmutableRef, error) {
	m.pins.Add(1)
	if m.afterPin != nil {
		m.afterPin()
	}
	return &partTestRef{id: id, manager: m}, nil
}

func (m *partTestManager) GetBySnapshotID(_ context.Context, id string, _ ...snapshots.RefOption) (snapshots.ImmutableRef, error) {
	return &partTestRef{id: id, manager: m}, nil
}

// partTestCache is transferTestCache with a snapshot manager that can answer
// a part's installation from an equivalent.
func partTestCache(t *testing.T) (context.Context, *Cache, *Server, *partTestManager) {
	t.Helper()
	ctx, c, srv := transferTestCache(t)
	manager := &partTestManager{}
	c.snapshotManager = manager
	return ctx, c, srv, manager
}

// partTestEncodedReceiver makes res an encoded, imported row with a pending
// snapshot part, which is what an imported row looks like before any demand.
func partTestEncodedReceiver(t *testing.T, ctx context.Context, c *Cache, res AnyResult) {
	t.Helper()
	record, err := c.CapturePersistedRecord(ctx, res)
	require.NoError(t, err)
	row := res.cacheSharedResult()
	row.payloadMu.Lock()
	row.hasValue, row.self = false, nil
	row.persistedEnvelope = &record.Envelope
	row.imported = true
	row.payloadRevision++
	row.payloadMu.Unlock()
}

func partTestAppliedLinks(res AnyResult) []PersistedSnapshotRefLink {
	row := res.cacheSharedResult()
	row.payloadMu.RLock()
	defer row.payloadMu.RUnlock()
	links, _ := row.snapshotLinksLocked()
	return cloneSnapshotRefLinks(links)
}

func partTestHasLink(res AnyResult, refKey string) bool {
	for _, link := range partTestAppliedLinks(res) {
		if link.RefKey == refKey {
			return true
		}
	}
	return false
}

// partTestPair builds an equivalent donor and an encoded imported receiver
// of the multi-part family and puts them in one output class.
func partTestPair(t *testing.T, ctx context.Context, c *Cache, srv *Server, donorParts, receiverParts map[string]partTestState) (AnyResult, AnyResult) {
	t.Helper()
	srv.InstallObject(NewClass(srv, ClassOpts[*partTestValue]{}))
	donor := persistedListTestResult(t, ctx, c, srv, "part-donor", newPartTestValue("donor", donorParts))
	receiver := persistedListTestResult(t, ctx, c, srv, "part-receiver", newPartTestValue("receiver", receiverParts))
	partTestEncodedReceiver(t, ctx, c, receiver)
	partTestEquivalent(t, c, receiver, donor)
	return donor, receiver
}

// partTestUnite teaches the rows the same content digest, which unites their
// output classes.
func partTestUnite(t *testing.T, ctx context.Context, c *Cache, label string, rows ...AnyResult) {
	t.Helper()
	content := digest.FromString(label)
	for _, row := range rows {
		require.NoError(t, c.TeachContentDigest(ctx, row, content))
	}
}
