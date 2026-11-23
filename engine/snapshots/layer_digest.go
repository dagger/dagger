package snapshots

import (
	"sync"

	digest "github.com/opencontainers/go-digest"
)

const (
	// layerDigestPiece is how much of what a layerDigester is written it
	// hands its hash at once.
	layerDigestPiece = 1 << 20
	// layerDigestPieces is how many pieces a layerDigester holds at once,
	// written and not yet hashed: how far its writer gets ahead of the hash,
	// and the most memory it takes, 8 MiB.
	layerDigestPieces = 8
)

// layerDigestPieceBuffers keeps pieces' buffers between digesters: a layer's
// export digests its tar and each of its larger files.
var layerDigestPieceBuffers = sync.Pool{New: func() any {
	b := make([]byte, 0, layerDigestPiece)
	return &b
}}

// layerDigester takes the SHA-256 digest of the bytes written to it: a
// layer's tar, for its diff ID, or a file's contents, for its content blob's
// uncompressed digest. The differ writes a layer through it beside the
// compressor, every byte of the layer once, on one goroutine; the digester
// hashes on a goroutine of its own, so that the writer goes on reading the
// layer's files while it hashes. Without it, a 6 GB layer's export spent
// about half its time on one core hashing in line, while its compression
// ran on others. The writer gets at most layerDigestPieces pieces ahead.
type layerDigester struct {
	digester digest.Digester
	// piece is the piece being filled.
	piece []byte
	// pieces carries filled pieces to the hash, in order, and room holds a
	// unit for each piece out of the writer's hands, until it is hashed.
	pieces chan []byte
	room   chan struct{}
	done   chan struct{}
	ended  bool
	// hashing, nil but in tests, runs before each piece is hashed.
	hashing func()
}

func newLayerDigester() *layerDigester {
	d := &layerDigester{
		digester: digest.SHA256.Digester(),
		pieces:   make(chan []byte, layerDigestPieces),
		room:     make(chan struct{}, layerDigestPieces),
		done:     make(chan struct{}),
	}
	go d.hash()
	return d
}

func (d *layerDigester) hash() {
	defer close(d.done)
	h := d.digester.Hash()
	for piece := range d.pieces {
		if d.hashing != nil {
			d.hashing()
		}
		h.Write(piece)
		piece = piece[:0]
		layerDigestPieceBuffers.Put(&piece)
		<-d.room
	}
}

// Write copies p into pieces for the hash. It never retains p.
func (d *layerDigester) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		if d.piece == nil {
			d.piece = (*layerDigestPieceBuffers.Get().(*[]byte))[:0]
		}
		take := min(layerDigestPiece-len(d.piece), len(p))
		d.piece = append(d.piece, p[:take]...)
		p = p[take:]
		if len(d.piece) == layerDigestPiece {
			d.send()
		}
	}
	return n, nil
}

// send hands the filled piece to the hash, once it has room.
func (d *layerDigester) send() {
	d.room <- struct{}{}
	d.pieces <- d.piece
	d.piece = nil
}

// end hands the hash what is left and waits for it to finish, once.
func (d *layerDigester) end() {
	if d.ended {
		return
	}
	d.ended = true
	if len(d.piece) > 0 {
		d.send()
	}
	close(d.pieces)
	<-d.done
}

// Digest returns the digest of everything written. Nothing may be written
// after it.
func (d *layerDigester) Digest() digest.Digest {
	d.end()
	return d.digester.Digest()
}

// Close releases the digester, whether or not its digest was taken: its
// hash ends.
func (d *layerDigester) Close() {
	d.end()
}
