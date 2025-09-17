package imageutil

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"testing"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/remotes"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestConfigMultiplatform(t *testing.T) {
	ctx := context.Background()

	cc := &testCache{}

	pAmd64 := platforms.MustParse("linux/amd64")
	cfgDescAmd64 := cc.Add(t, ocispecs.Image{Platform: pAmd64}, ocispecs.MediaTypeImageConfig, nil)
	mfstAmd64 := ocispecs.Manifest{MediaType: ocispecs.MediaTypeImageManifest, Config: cfgDescAmd64}

	// Add linux/amd64 to the cache
	descAmd64 := cc.Add(t, mfstAmd64, mfstAmd64.MediaType, &pAmd64)

	// Make a compatible manifest but do not add to the cache
	p386 := platforms.MustParse("linux/386")
	cfgDesc386 := cc.Add(t, ocispecs.Image{Platform: p386}, ocispecs.MediaTypeImageConfig, nil)
	mfst386 := ocispecs.Manifest{MediaType: ocispecs.MediaTypeImageManifest, Config: cfgDesc386}
	_, desc386 := makeDesc(t, mfst386, mfst386.MediaType, &p386)

	// And add an extra non-compataible platform
	pArm64 := platforms.MustParse("linux/arm64")
	cfgDescArm64 := cc.Add(t, ocispecs.Image{Platform: pArm64}, ocispecs.MediaTypeImageConfig, nil)
	mfstArm64 := ocispecs.Manifest{MediaType: ocispecs.MediaTypeImageManifest, Config: cfgDescArm64}
	_, descArm64 := makeDesc(t, mfstArm64, mfstArm64.MediaType, &pArm64)

	idx := ocispecs.Index{
		MediaType: ocispecs.MediaTypeImageIndex,
		Manifests: []ocispecs.Descriptor{desc386, descAmd64, descArm64},
	}

	check := func(t *testing.T) {
		t.Helper()

		idxDesc := cc.Add(t, idx, idx.MediaType, nil)
		r := &testResolver{cc: cc, resolve: func(ctx context.Context, ref string) (string, ocispecs.Descriptor, error) {
			return ref, idxDesc, nil
		}}

		// Now we should be able to get the amd64 config without fetching anything from the remote
		// If it tries to fetch from the remote this will error out.
		const ref = "example.com/test:latest"
		_, dt, err := Config(ctx, ref, r, cc, nil, &pAmd64)
		require.NoError(t, err)

		var cfg ocispecs.Image
		require.NoError(t, json.Unmarshal(dt, &cfg))
		// Validate this is the amd64 config
		require.True(t, platforms.OnlyStrict(cfg.Platform).Match(pAmd64))

		// Make sure it doesn't select a non-matching platform
		pArmv7 := platforms.MustParse("linux/arm/v7")
		_, _, err = Config(ctx, ref, r, cc, nil, &pArmv7)
		require.ErrorIs(t, err, cerrdefs.ErrNotFound)
	}

	check(t)

	// Shuffle the manifests around and make sure it still works
	rand.Shuffle(len(idx.Manifests), func(i, j int) {
		idx.Manifests[i], idx.Manifests[j] = idx.Manifests[j], idx.Manifests[i]
	})
	check(t)
}

type testCache struct {
	content.Manager
	content map[digest.Digest]content.ReaderAt
}

func (testCache) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	return content.Info{}, nil
}

func (testCache) Update(context.Context, content.Info, ...string) (content.Info, error) {
	return content.Info{}, nil
}

func (*testCache) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	// This needs to be implemented because the content helpers will open a writer to use as a lock
	return nopWriter{}, nil
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (nopWriter) Close() error {
	return nil
}

func (nopWriter) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...content.Opt) error {
	return nil
}

func (nopWriter) Status() (content.Status, error) {
	return content.Status{}, nil
}

func (nopWriter) Truncate(size int64) error {
	return nil
}

func (nopWriter) Digest() digest.Digest {
	panic("not implemented")
}

func makeDesc(t *testing.T, item any, mt string, p *ocispecs.Platform) ([]byte, ocispecs.Descriptor) {
	t.Helper()

	dt, err := json.Marshal(item)
	require.NoError(t, err)

	return dt, ocispecs.Descriptor{
		MediaType: mt,
		Digest:    digest.FromBytes(dt),
		Size:      int64(len(dt)),
		Platform:  p,
	}
}

func (c *testCache) Add(t *testing.T, item any, mt string, p *ocispecs.Platform) ocispecs.Descriptor {
	if c.content == nil {
		c.content = make(map[digest.Digest]content.ReaderAt)
	}

	dt, desc := makeDesc(t, item, mt, p)
	c.content[desc.Digest] = &sectionNopCloser{io.NewSectionReader(bytes.NewReader(dt), 0, int64(len(dt)))}
	return desc
}

type sectionNopCloser struct {
	*io.SectionReader
}

func (*sectionNopCloser) Close() error {
	return nil
}

func (c *testCache) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	ra, ok := c.content[desc.Digest]
	if !ok {
		return nil, cerrdefs.ErrNotFound
	}
	return ra, nil
}

type testResolver struct {
	resolve func(ctx context.Context, ref string) (string, ocispecs.Descriptor, error)
	cc      ContentCache
}

type fetcherFunc func(context.Context, ocispecs.Descriptor) (io.ReadCloser, error)

func (f fetcherFunc) Fetch(ctx context.Context, desc ocispecs.Descriptor) (io.ReadCloser, error) {
	return f(ctx, desc)
}

func (r *testResolver) Fetcher(ctx context.Context, ref string) (remotes.Fetcher, error) {
	return fetcherFunc(func(ctx context.Context, desc ocispecs.Descriptor) (io.ReadCloser, error) {
		ra, err := r.cc.ReaderAt(ctx, desc)
		if err != nil {
			return nil, err
		}
		return &raReader{ReaderAt: ra, Closer: ra}, nil
	}), nil
}

type raReader struct {
	io.ReaderAt
	io.Closer

	pos int
}

func (r *raReader) Read(p []byte) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, int64(r.pos))
	r.pos += n
	return n, err
}

func (r *testResolver) Resolve(ctx context.Context, ref string) (string, ocispecs.Descriptor, error) {
	return r.resolve(ctx, ref)
}

func (r *testResolver) Pusher(context.Context, string) (remotes.Pusher, error) {
	panic("not implemented")
}
