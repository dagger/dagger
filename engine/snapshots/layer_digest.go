package snapshots

import (
	"sync"

	digest "github.com/opencontainers/go-digest"
)

const (
	// layerDigestPiece is how much of what a layerDigester is written it
	// hands its hash at once.
	layerDigestPiece = 1 << 20
	// layerDigestPieces is how many pieces a layerDigester hands its hash
	// and hasn't hashed yet: how far its writer gets ahead of the hash. With
	// the piece it is filling, a digester holds at most 9 MiB.
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
// The goroutine starts with the first full piece: what never fills one, as
// most files' contents, is hashed in line when its digest is taken.
type layerDigester struct {
	digester digest.Digester
	// piece is the piece being filled.
	piece []byte
	// pieces carries filled pieces to the hash, in order, and room holds a
	// unit for each piece out of the writer's hands, until it is hashed.
	// They are nil until the first piece fills, and done closes once the
	// hash has hashed every piece.
	pieces chan []byte
	room   chan struct{}
	done   chan struct{}
	ended  bool
	// hashing, nil but in tests, runs before each piece is hashed.
	hashing func()
}

func newLayerDigester() *layerDigester {
	return &layerDigester{digester: digest.SHA256.Digester()}
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

// send hands the filled piece to the hash, once it has room, starting the
// hash with the first.
func (d *layerDigester) send() {
	if d.pieces == nil {
		d.pieces = make(chan []byte, layerDigestPieces)
		d.room = make(chan struct{}, layerDigestPieces)
		d.done = make(chan struct{})
		go d.hash()
	}
	d.room <- struct{}{}
	d.pieces <- d.piece
	d.piece = nil
}

// end hashes what is left and waits for the hash to finish, once. What
// never filled a piece is hashed here, with no goroutine.
func (d *layerDigester) end() {
	if d.ended {
		return
	}
	d.ended = true
	if d.pieces == nil {
		if len(d.piece) > 0 {
			if d.hashing != nil {
				d.hashing()
			}
			d.digester.Hash().Write(d.piece)
		}
		if d.piece != nil {
			piece := d.piece[:0]
			layerDigestPieceBuffers.Put(&piece)
			d.piece = nil
		}
		return
	}
	if len(d.piece) > 0 {
		d.send()
	} else if d.piece != nil {
		piece := d.piece[:0]
		layerDigestPieceBuffers.Put(&piece)
		d.piece = nil
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
