package snapshots

import (
	digest "github.com/opencontainers/go-digest"
)

// layerDigester takes the SHA-256 digest of the bytes written to it: a
// layer's tar, for its diff ID, or a file's contents, for its content blob's
// uncompressed digest. The differ writes a layer through it beside the
// compressor, every byte of the layer once.
type layerDigester struct {
	digester digest.Digester
	// hashing, nil but in tests, runs before each piece is hashed.
	hashing func()
}

func newLayerDigester() *layerDigester {
	return &layerDigester{digester: digest.SHA256.Digester()}
}

func (d *layerDigester) Write(p []byte) (int, error) {
	if d.hashing != nil {
		d.hashing()
	}
	return d.digester.Hash().Write(p)
}

// Digest returns the digest of everything written.
func (d *layerDigester) Digest() digest.Digest {
	return d.digester.Digest()
}

// Close releases the digester, whether or not its digest was taken.
func (d *layerDigester) Close() {}
