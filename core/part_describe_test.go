package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// requireDescribedAsEncoded checks that obj describes its parts from its live
// value as DescribeParts describes its encoding: the same complete parts and
// snapshot links, or a failure where encoding it or describing its encoding
// fails. It returns whether obj described itself; when it defers, its
// encoding is described instead, so there is nothing to compare.
func requireDescribedAsEncoded(t testing.TB, ctx context.Context, enc *dagql.PersistEncodeContext, obj interface {
	dagql.PersistedObject
	dagql.PersistedPartsDescriber
}) bool {
	t.Helper()
	live, described, liveErr := obj.DescribePersistedParts(ctx, enc)
	if liveErr == nil && !described {
		return false
	}
	encoding, err := obj.EncodePersistedObject(ctx, enc)
	var probes []dagql.PartProbe
	if err == nil {
		v := enc.PayloadVisit(encoding.SnapshotLinks)
		v.Payload = encoding.JSON
		probes, err = foreignFamilyCodec(obj.Type().Name()).DescribeParts(v)
	}
	if err != nil {
		require.Error(t, liveErr, "describing the encoding fails: %v", err)
		return false
	}
	require.NoError(t, liveErr)
	var complete []dagql.PersistedPartAddress
	for _, probe := range probes {
		if probe.LocalComplete {
			complete = append(complete, probe.Descriptor.Address)
		}
	}
	require.Equal(t, complete, live.Complete)
	require.Equal(t, encoding.SnapshotLinks, live.SnapshotLinks)
	return true
}
