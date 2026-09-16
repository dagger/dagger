package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql/persistdb"
	"github.com/stretchr/testify/require"
)

func scopedSnapshotRecord() PersistedRecord {
	leaf := PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindObject, ObjectCodec: "dagql_test.PersistSnapshotValue", TypeName: "PersistSnapshotValue", ObjectJSON: json.RawMessage(`{"name":"scoped"}`)}
	return PersistedRecord{ResultID: 7, Envelope: PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindList, ResultID: 7, Items: []PersistedResultEnvelope{leaf, leaf, {Version: persistedResultEnvelopeVersion, Kind: persistedResultKindRef, ResultID: 8}}}, SnapshotLinks: []PersistedSnapshotRefLink{{OutputPath: PersistedRefPath{}.Field("items").Index(0), Role: "snapshot", RefKey: "a"}, {OutputPath: PersistedRefPath{}.Field("items").Index(1), Role: "snapshot", RefKey: "b"}}}
}
func TestScopedSnapshotVisitor(t *testing.T) {
	rec := scopedSnapshotRecord()
	out, err := VisitEncodedReferences(rec, func(ref *PersistedRef) error {
		if ref.RefKey == "a" {
			ref.RefKey = "new-a"
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "new-a", out.SnapshotLinks[0].RefKey)
	require.Equal(t, "b", out.SnapshotLinks[1].RefKey)
	require.Equal(t, "a", rec.SnapshotLinks[0].RefKey)
	out.SnapshotLinks[0].OutputPath[0].Field = "changed"
	require.Equal(t, "items", rec.SnapshotLinks[0].OutputPath[0].Field)
	sentinel := errors.New("stop after first rewrite")
	out, err = VisitEncodedReferences(rec, func(ref *PersistedRef) error {
		if ref.RefKey == "a" {
			ref.RefKey = "lost"
		}
		if ref.RefKey == "b" {
			return sentinel
		}
		return nil
	})
	require.ErrorIs(t, err, sentinel)
	require.Empty(t, out)
	require.Equal(t, "a", rec.SnapshotLinks[0].RefKey)
	for _, path := range []PersistedRefPath{nil, PersistedRefPath{}.Field("items").Index(3), PersistedRefPath{}.Field("items").Index(2), PersistedRefPath{}.Field("items").Index(2).Field("items").Index(0), PersistedRefPath{}.Field("items").Index(0).Field("objectJSON"), {{Field: "items", Index: 1}}, {{IsIndex: true, Index: -1}}} {
		bad := rec
		bad.SnapshotLinks = cloneSnapshotRefLinks(rec.SnapshotLinks)
		bad.SnapshotLinks[0].OutputPath = path
		_, err := VisitEncodedReferences(bad, func(*PersistedRef) error { return nil })
		require.Error(t, err, "path %v", path)
	}
	dup := rec
	dup.SnapshotLinks = append(cloneSnapshotRefLinks(rec.SnapshotLinks), rec.SnapshotLinks[0])
	_, err = VisitEncodedReferences(dup, func(*PersistedRef) error { return nil })
	require.ErrorContains(t, err, "declared twice")
	for _, kind := range []string{persistedResultKindNull, persistedResultKindScalar, persistedResultKindList} {
		bad := scopedSnapshotRecord()
		bad.Envelope.Items[0] = PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: kind}
		_, err := VisitEncodedReferences(bad, func(*PersistedRef) error { return nil })
		require.Error(t, err)
	}
	for _, mutate := range []func(*PersistedRef){func(r *PersistedRef) { r.Role = "changed" }, func(r *PersistedRef) { r.Path = nil }} {
		_, err := VisitEncodedReferences(rec, func(r *PersistedRef) error {
			if r.RefKey != "" {
				mutate(r)
			}
			return nil
		})
		require.Error(t, err)
	}
}
func TestScopedSnapshotDecodeCarrier(t *testing.T) {
	rec := scopedSnapshotRecord()
	root := NewPersistDecodeContext(nil, 7, nil).WithSnapshotRoles(rec.SnapshotLinks)
	roles, err := root.SnapshotRoles(t.Context())
	require.NoError(t, err)
	require.Empty(t, roles)
	one, two := root.item(nil, 0), root.item(nil, 1)
	require.Zero(t, one.ResultID())
	require.EqualValues(t, 7, one.SnapshotScope().OwnerResultID)
	a, err := one.SnapshotRole(t.Context(), "snapshot")
	require.NoError(t, err)
	require.Equal(t, "a", a.RefKey)
	require.Empty(t, a.OutputPath)
	b, err := two.SnapshotRole(t.Context(), "snapshot")
	require.NoError(t, err)
	require.Equal(t, "b", b.RefKey)
	empty := root.WithSnapshotRoles(nil).item(nil, 0)
	_, err = empty.SnapshotRole(t.Context(), "snapshot")
	require.ErrorContains(t, err, "missing persisted snapshot link")
	_, err = NewPersistDecodeContext(nil, 7, nil).item(nil, 0).SnapshotRoles(t.Context())
	require.ErrorContains(t, err, "missing owner carrier")
	mismatch := *one
	mismatch.roles = &copiedDecodeRoles{ResultID: 8}
	_, err = mismatch.SnapshotRoles(t.Context())
	require.ErrorContains(t, err, "mismatch")
	copy := one.SnapshotScope()
	copy.OutputPath[0].Field = "changed"
	require.Equal(t, "items", one.SnapshotScope().OutputPath[0].Field)
}
func TestScopedSnapshotCanonicalKeys(t *testing.T) {
	root, err := canonicalPath(nil)
	require.NoError(t, err)
	require.Equal(t, "[]", root)
	empty, err := canonicalPath(PersistedRefPath{})
	require.NoError(t, err)
	require.Equal(t, root, empty)
	a, _ := canonicalPath(PersistedRefPath{}.Field("items[0].x"))
	b, _ := canonicalPath(PersistedRefPath{}.Field("items").Index(0).Field("x"))
	require.NotEqual(t, a, b)
	zero, _ := canonicalPath(PersistedRefPath{}.Index(0))
	require.Equal(t, `[{"isIndex":true}]`, zero)
	require.NotEqual(t, resultSnapshotLeaseID(7, `[[],"snapshot"]`), resultSnapshotLeaseID(7, "snapshot"))
	require.NotEqual(t, resultSnapshotLeaseID(7, "snapshot"), resultSnapshotLeaseID(7, "snapshot", PersistedRefPath{}.Field("items").Index(0)))
}
func TestScopedSnapshotSQLConflict(t *testing.T) {
	ctx := context.Background()
	db, q, err := prepareCacheDBs(ctx, filepath.Join(t.TempDir(), "cache.db"))
	require.NoError(t, err)
	defer closeCacheDBs(db, q)
	require.NoError(t, q.InsertMirrorResult(ctx, persistdb.MirrorResult{ID: 1, SelfPayload: []byte(`{}`)}))
	link := persistdb.MirrorResultSnapshotLink{ResultID: 1, OutputPath: "[]", Role: "snapshot", RefKey: "first"}
	require.NoError(t, q.InsertMirrorResultSnapshotLink(ctx, link))
	require.Error(t, q.InsertMirrorResultSnapshotLink(ctx, link))
	link.RefKey = "replacement"
	require.Error(t, q.InsertMirrorResultSnapshotLink(ctx, link))
	stored, err := q.ListMirrorResultSnapshotLinks(ctx)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.Equal(t, "first", stored[0].RefKey)
}

func TestScopedDecodeRejectsMismatchedCarrierBeforeCodec(t *testing.T) {
	ctx := context.WithValue(t.Context(), copiedDecodeRolesKey{}, &copiedDecodeRoles{ResultID: 2})
	_, err := DefaultPersistedSelfCodec.DecodeResult(ctx, nil, 1, nil, PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindNull, ResultID: 1})
	require.ErrorContains(t, err, "carrier owner mismatch")
}
