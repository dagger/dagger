package dagql

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// partOwnerValue holds a link per role, readable whole or by part, as a
// Container is: part "p:<role>" links under role <role>.
type partOwnerValue struct {
	persistSnapshotValue
	links      map[string]PersistedSnapshotRefLink
	wholeReads int
	partReads  int
	scopable   bool
}

func (v *partOwnerValue) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink {
	var out []PersistedSnapshotRefLink
	for _, link := range v.links {
		out = append(out, link)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role < out[j].Role })
	return out
}

func (v *partOwnerValue) ReadSnapshotOwner(context.Context) (OutputRevision, []PersistedSnapshotRefLink, error) {
	v.wholeReads++
	return 1, v.PersistedSnapshotRefLinks(), nil
}

func (v *partOwnerValue) ReadSnapshotOwnerParts(parts []PartKey) ([]PersistedSnapshotRefLink, []string, bool) {
	v.partReads++
	if !v.scopable {
		return nil, nil, false
	}
	var links []PersistedSnapshotRefLink
	var roles []string
	for _, part := range parts {
		role := string(part)[len("p:"):]
		roles = append(roles, role)
		if link, ok := v.links[role]; ok {
			links = append(links, link)
		}
	}
	return links, roles, true
}

func installedParts(parts ...PartKey) *InstalledOutputs {
	installed := &InstalledOutputs{}
	for _, part := range parts {
		installed.outputs = append(installed.outputs, installedPartOutput{address: PersistedPartAddress{Part: part}})
	}
	return installed
}

func TestSyncInstalledPartLeases(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	snapshots := &fakeSnapshotManager{}
	c, err := NewCache(ctx, "", snapshots, nil)
	require.NoError(t, err)
	session := cacheTestSessionID(t, ctx)
	value := &partOwnerValue{scopable: true, links: map[string]PersistedSnapshotRefLink{
		"a": {RefKey: "a1", Role: "a"},
		"b": {RefKey: "b1", Role: "b"},
		"c": {RefKey: "c1", Role: "c", Borrowed: true},
	}}
	call := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(value.Type()), Field: "parts"}
	res, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(value), nil
	})
	require.NoError(t, err)
	row := res.cacheSharedResult()
	require.Equal(t, 1, value.wholeReads, "publication reads the value whole")
	require.Len(t, snapshots.attachCalls, 2, "a and b get leases; borrowed c doesn't")

	// Part a is refilled with another snapshot and part b changes too, but
	// only a was filled: b keeps its lease until a sync that reads it.
	snapshots.attachCalls, snapshots.removeCalls = nil, nil
	value.links["a"] = PersistedSnapshotRefLink{RefKey: "a2", Role: "a"}
	value.links["b"] = PersistedSnapshotRefLink{RefKey: "b2", Role: "b"}
	require.NoError(t, c.syncInstalledPartLeases(ctx, row, installedParts("p:a"), nil))
	require.Equal(t, 1, value.wholeReads, "the part is read alone")
	require.Equal(t, 1, value.partReads)
	require.Len(t, snapshots.removeCalls, 1)
	require.Len(t, snapshots.attachCalls, 1)
	require.Equal(t, "a2", snapshots.attachCalls[0].SnapshotID)
	stored := map[string]string{}
	for _, link := range row.loadSnapshotOwnerLinks() {
		stored[link.Role] = link.RefKey
	}
	require.Equal(t, map[string]string{"a": "a2", "b": "b1", "c": "c1"}, stored)

	// A filled part that is unchanged attaches nothing; a part that lost its
	// snapshot loses its lease.
	snapshots.attachCalls, snapshots.removeCalls = nil, nil
	delete(value.links, "a")
	require.NoError(t, c.syncInstalledPartLeases(ctx, row, installedParts("p:a"), []PartKey{"p:c"}))
	require.Empty(t, snapshots.attachCalls)
	require.Len(t, snapshots.removeCalls, 1)

	// A value that can't be read by part, or parts below the row, are synced
	// whole.
	value.scopable = false
	require.NoError(t, c.syncInstalledPartLeases(ctx, row, installedParts("p:b"), nil))
	require.Equal(t, 2, value.wholeReads)
	value.scopable = true
	nested := installedParts("p:b")
	nested.outputs[0].address.OutputPath = PersistedRefPath{}.Field("items")
	require.NoError(t, c.syncInstalledPartLeases(ctx, row, nested, nil))
	require.Equal(t, 3, value.wholeReads)
	require.NoError(t, c.syncInstalledPartLeases(ctx, row, nil, nil))
	require.Equal(t, 4, value.wholeReads)

	require.NoError(t, c.ReleaseSession(ctx, session))
	require.NoError(t, c.Close(ctx))
}
