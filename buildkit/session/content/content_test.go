package content

import (
	"context"
	"testing"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/testutil"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestContentAttachable(t *testing.T) {
	ctx := context.TODO()
	t.Parallel()
	ids := []string{"store-id-0", "store-id-1"}
	attachableStores := make(map[string]content.Store)
	testBlobs := make(map[string]map[digest.Digest][]byte)
	for _, id := range ids {
		store, err := local.NewStore(t.TempDir())
		require.NoError(t, err)
		blob := []byte("test-content-attachable-" + id)
		w, err := store.Writer(ctx, content.WithRef(string(blob)))
		require.NoError(t, err)
		n, err := w.Write(blob)
		require.NoError(t, err)
		err = w.Commit(ctx, int64(n), "")
		require.NoError(t, err)
		err = w.Close()
		require.NoError(t, err)
		blobDigest := w.Digest()
		attachableStores[id] = store
		testBlobs[id] = map[digest.Digest][]byte{
			blobDigest: blob,
		}
	}

	s, err := session.NewSession(ctx, "foo", "bar")
	require.NoError(t, err)

	m, err := session.NewManager()
	require.NoError(t, err)

	a := NewAttachable(attachableStores)
	s.Allow(a)

	dialer := session.Dialer(testutil.TestStream(testutil.Handler(m.HandleConn)))

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return s.Run(ctx, dialer)
	})

	g.Go(func() error {
		c, err := m.Get(ctx, s.ID(), false)
		if err != nil {
			return err
		}
		for storeID, blobMap := range testBlobs {
			callerStore := NewCallerStore(c, storeID)
			for dgst, blob := range blobMap {
				blob2, err := content.ReadBlob(ctx, callerStore, ocispecs.Descriptor{Digest: dgst})
				if err != nil {
					return err
				}
				assert.Equal(t, blob, blob2)
			}
		}
		return s.Close()
	})

	werr := g.Wait()
	require.NoError(t, werr)
}
