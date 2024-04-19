package contentutil

import (
	"bytes"
	"context"
	"testing"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestMultiProvider(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	b0 := NewBuffer()
	b1 := NewBuffer()

	err := content.WriteBlob(ctx, b0, "foo", bytes.NewBuffer([]byte("foo0")), ocispecs.Descriptor{Size: -1})
	require.NoError(t, err)

	err = content.WriteBlob(ctx, b1, "foo", bytes.NewBuffer([]byte("foo1")), ocispecs.Descriptor{Size: -1})
	require.NoError(t, err)

	mp := NewMultiProvider(nil)
	mp.Add(digest.FromBytes([]byte("foo0")), b0)
	mp.Add(digest.FromBytes([]byte("foo1")), b1)

	dt, err := content.ReadBlob(ctx, mp, ocispecs.Descriptor{Digest: digest.FromBytes([]byte("foo0"))})
	require.NoError(t, err)
	require.Equal(t, string(dt), "foo0")

	dt, err = content.ReadBlob(ctx, mp, ocispecs.Descriptor{Digest: digest.FromBytes([]byte("foo1"))})
	require.NoError(t, err)
	require.Equal(t, string(dt), "foo1")

	_, err = content.ReadBlob(ctx, mp, ocispecs.Descriptor{Digest: digest.FromBytes([]byte("foo2"))})
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, errdefs.ErrNotFound))
}
