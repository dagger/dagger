package container

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/cache"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/stretchr/testify/require"
)

type trackingMutableRef struct {
	cache.MutableRef
	releaseCalls int
}

func (r *trackingMutableRef) Release(context.Context) error {
	r.releaseCalls++
	return nil
}

func TestPrepareMountsReleasesPartialRefsOnError(t *testing.T) {
	t.Parallel()

	prepareErr := errors.New("prepare mounts failed")
	activeRef := &trackingMutableRef{}
	outputRef := &trackingMutableRef{}

	callNum := 0
	makeMutable := func(*pb.Mount, cache.ImmutableRef) (cache.MutableRef, error) {
		callNum++
		switch callNum {
		case 1:
			return activeRef, nil
		case 2:
			return outputRef, nil
		default:
			return nil, prepareErr
		}
	}

	_, err := PrepareMounts(context.Background(), nil, nil, nil, "", []*pb.Mount{
		{Dest: "/active", MountType: pb.MountType_BIND, Input: pb.Empty, Output: pb.SkipOutput},
		{Dest: "/output", MountType: pb.MountType_BIND, Input: pb.Empty, Output: 0},
		{Dest: "/fail", MountType: pb.MountType_BIND, Input: pb.Empty, Output: 0},
	}, nil, makeMutable, runtime.GOOS)
	require.ErrorIs(t, err, prepareErr)
	require.Equal(t, 1, activeRef.releaseCalls)
	require.Equal(t, 1, outputRef.releaseCalls)
}
