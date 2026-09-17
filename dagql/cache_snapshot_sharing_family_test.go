package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/dagger/dagger/engine/snapshots"

	"github.com/vektah/gqlparser/v2/ast"
)

// shareTestValue is a multi-part transfer value: one payload declaring
// several named parts, each pending or completed with its own snapshot. It
// exists so the sharing cases can exercise several addresses of one receiver
// without a real store or a core type.
type sharePartState struct {
	Snapshot string `json:"snapshot,omitempty"`
	Absent   bool   `json:"absent,omitempty"`
	// Service is an exact Service row the donated descriptor carries, which
	// is what makes a typed preparation need the engine's registered
	// reconstruction context.
	Service uint64 `json:"service,omitempty"`
}

type shareTestValue struct {
	Name  string                    `json:"name"`
	Parts map[string]sharePartState `json:"parts,omitempty"`
	// mu guards Parts, links and rev the way a core value's output guard
	// does: readers copy under it, and a prepared store holds it from TryLock
	// to Unlock so its Publish cannot race an encoder.
	mu    sync.RWMutex
	rev   atomic.Uint64
	links []PersistedSnapshotRefLink
	// afterStoreUnlock runs when a prepared store that published is unlocked:
	// the first point after a typed Commit's publication that test code sees.
	afterStoreUnlock func()
}

type shareTestEncoded struct {
	Name  string                    `json:"name"`
	Parts map[string]sharePartState `json:"parts,omitempty"`
}

func (v *shareTestValue) payloadLocked() shareTestEncoded {
	parts := make(map[string]sharePartState, len(v.Parts))
	for name, state := range v.Parts {
		parts[name] = state
	}
	return shareTestEncoded{Name: v.Name, Parts: parts}
}

func newShareTestValue(name string, parts map[string]sharePartState) *shareTestValue {
	v := &shareTestValue{Name: name, Parts: parts}
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

func (*shareTestValue) Type() *ast.Type {
	return &ast.Type{NamedType: "ShareTestValue", NonNull: true}
}

func (v *shareTestValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	raw, err := json.Marshal(v.payloadLocked())
	return PersistedObjectEncoding{JSON: raw, SnapshotLinks: cloneSnapshotRefLinks(v.links)}, err
}

func (v *shareTestValue) PersistedOutputRevision() (OutputRevision, error) {
	return OutputRevision(v.rev.Load()), nil
}

// PersistedSnapshotRefLinks is what makes an ordinary publication apply this
// value's owner links, exactly as a completed core value does.
func (v *shareTestValue) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return cloneSnapshotRefLinks(v.links)
}

// PreparePartStore is the typed receiver's writer: a closed assignment that
// cannot fail, allocate storage or run cleanup once prepared, with its own
// expected output revision, exactly like the File and Directory stores.
func (v *shareTestValue) PreparePartStore(_ context.Context, _ *PersistDecodeContext, _ PersistedRecord, d PartDescriptor, ref snapshots.ImmutableRef) (PreparedPartStore, error) {
	if d.SnapshotID == "" {
		return nil, fmt.Errorf("share test store: donated part %q has no snapshot", d.Address.Part)
	}
	revision, err := v.PersistedOutputRevision()
	if err != nil {
		return nil, err
	}
	return &shareTestPartStore{receiver: v, part: string(d.Address.Part), snapshot: d.SnapshotID, expected: revision, ref: ref}, nil
}

type shareTestPartStore struct {
	published bool
	receiver  *shareTestValue
	part      string
	snapshot  string
	expected  OutputRevision
	ref       snapshots.ImmutableRef
}

func (s *shareTestPartStore) TryLock() bool {
	if !s.receiver.mu.TryLock() {
		return false
	}
	if OutputRevision(s.receiver.rev.Load()) != s.expected {
		s.receiver.mu.Unlock()
		return false
	}
	return true
}

func (s *shareTestPartStore) Unlock() {
	s.receiver.mu.Unlock()
	if s.published && s.receiver.afterStoreUnlock != nil {
		s.receiver.afterStoreUnlock()
	}
}

func (s *shareTestPartStore) Publish() {
	s.published = true
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

func (*shareTestValue) DecodePersistedObject(ctx context.Context, dec *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	value := new(shareTestValue)
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

type shareTestCodec struct{}

func (shareTestCodec) VisitPersistedReferences(v PersistedPayloadVisit, visit PersistedRefVisitor) (json.RawMessage, error) {
	if err := VisitPersistedSnapshotRoles(visit, PersistedRefOutputRole, v.Path, v.SnapshotLinks); err != nil {
		return nil, err
	}
	return v.Payload, nil
}

func (shareTestCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: slices.Clone(v.Payload)}, nil
}

func (shareTestCodec) ValidateForeign(PersistedPayloadVisit) error { return nil }

func shareTestPayload(v PersistedPayloadVisit) (*shareTestValue, error) {
	value := new(shareTestValue)
	if err := json.Unmarshal(v.Payload, value); err != nil {
		return nil, err
	}
	return value, nil
}

func shareTestRoleKey(links []PersistedSnapshotRefLink, role string) string {
	for _, link := range links {
		if link.Role == role {
			return link.RefKey
		}
	}
	return ""
}

func (shareTestCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	value, err := shareTestPayload(v)
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
			o.Value = shareTestSnapshotValue(state)
		default:
			o.Value = shareTestSnapshotValue(state)
		}
		out = append(out, o)
	}
	return out, nil
}

func shareTestSnapshotValue(state sharePartState) *SnapshotValue {
	value := &SnapshotValue{Kind: "directory"}
	if state.Service != 0 {
		value.Services = []TransferredServiceBinding{{ServiceResultID: state.Service, Hostname: "share-test"}}
	}
	return value
}

// RouteParts lets an ordinary demand route one of this family's parts: it has
// no metadata part, no delegation and no saved Lazy operation, so a demand
// can only take a completed equivalent.
func (shareTestCodec) RouteParts(PersistedPayloadVisit, PartKey) (LazyOperationRoute, error) {
	return LazyOperationRoute{}, nil
}

func (shareTestCodec) DescribeParts(v PersistedPayloadVisit) ([]PartProbe, error) {
	outputs, err := (shareTestCodec{}).MapSnapshotParts(v)
	if err != nil {
		return nil, err
	}
	probes := make([]PartProbe, 0, len(outputs))
	for _, o := range outputs {
		probes = append(probes, PartProbe{
			Descriptor:    PartDescriptor{Address: o.Address, Value: o.Value, Absent: o.State == "absent", SnapshotID: shareTestRoleKey(v.SnapshotLinks, o.Role)},
			LocalComplete: o.State == "completed" || o.State == "absent",
		})
	}
	return probes, nil
}

// PreparePartRecord installs one donated part into the receiver's own
// representation, keeping every other declared part and role unchanged.
func (shareTestCodec) PreparePartRecord(receiver, source PersistedRecord, d PartDescriptor, target PersistedPartAddress) (PersistedRecord, error) {
	value := new(shareTestValue)
	if err := json.Unmarshal(receiver.Envelope.ObjectJSON, value); err != nil {
		return receiver, err
	}
	part := string(target.Part)
	state, ok := value.Parts[part]
	if !ok {
		return receiver, fmt.Errorf("share test: receiver has no part %q", part)
	}
	if d.SnapshotID == "" {
		return receiver, fmt.Errorf("share test: donated part %q has no snapshot", part)
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

func maps2Keys(m map[string]sharePartState) func(func(string) bool) {
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
		Name:             "dagql_test.Share",
		Typed:            (*shareTestValue)(nil),
		Visitor:          shareTestCodec{},
		Transfer:         shareTestCodec{},
		BackgroundDecode: true,
	})
}
