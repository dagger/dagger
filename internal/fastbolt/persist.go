package bbolt

import (
	"bufio"
	"encoding/binary"
	stderrors "errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"

	"go.etcd.io/bbolt/errors"
)

// The file holds the whole database as of the last Close:
//
//	magic (8 bytes) | state (1 byte) | bucket | CRC-32C of the bucket (4 bytes)
//
// A bucket is its sequence, its entry count, and each entry in key order: the
// key's length and bytes, then 0 with the value's length and bytes, or 1 with
// a nested bucket. Lengths, sequences and counts are uvarints.
//
// Open marks the file in use by rewriting its state byte, and Close replaces
// the whole file with a clean one. A file still marked in use was opened by a
// process that never closed it, so its contents are older than what that
// process saw: Open refuses it, and the engine resets its local cache.
var magic = [8]byte{'d', 'g', 'm', 'e', 'm', 'd', 'b', 1}

const (
	stateClean byte = 'C'
	stateInUse byte = 'U'
	stateOff        = int64(len(magic))
)

// ErrUncleanShutdown is returned by Open for a database its last user opened
// and never closed.
var ErrUncleanShutdown = stderrors.New("database was not closed cleanly")

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Open loads the database at path, or starts an empty one if the file is
// missing or empty, and marks the file in use until Close.
func Open(path string, mode os.FileMode, options *Options) (*DB, error) {
	db := &DB{path: path, mode: mode}
	root := &bucketData{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil && len(data) > 0:
		if root, db.ids, err = decodeFile(data); err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return nil, err
		}
		_, err = f.WriteAt([]byte{stateInUse}, stateOff)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
	case err == nil || os.IsNotExist(err):
		if err := writeFile(path, mode, stateInUse, root); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	db.root.Store(root)
	return db, nil
}

// Close waits for the writer, then writes the database to its file, marked
// clean. A closed database refuses new transactions; readers already running
// keep their view.
func (db *DB) Close() error {
	db.wmu.Lock()
	defer db.wmu.Unlock()
	if db.closed.Swap(true) {
		return nil
	}
	if db.path == "" {
		return nil
	}
	return writeFile(db.path, db.mode, stateClean, db.root.Load())
}

func writeFile(path string, mode os.FileMode, state byte, root *bucketData) (err error) {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	w := bufio.NewWriterSize(f, 1<<20)
	w.Write(magic[:])
	w.WriteByte(state)
	h := crc32.New(castagnoli)
	e := encoder{w: io.MultiWriter(w, h)}
	e.bucket(root)
	if e.err != nil {
		return e.err
	}
	w.Write(binary.LittleEndian.AppendUint32(nil, h.Sum32()))
	if err := w.Flush(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type encoder struct {
	w   io.Writer
	buf [binary.MaxVarintLen64]byte
	err error
}

func (e *encoder) uvarint(v uint64) {
	if e.err == nil {
		_, e.err = e.w.Write(binary.AppendUvarint(e.buf[:0], v))
	}
}

func (e *encoder) bytes(b []byte) {
	e.uvarint(uint64(len(b)))
	if e.err == nil {
		_, e.err = e.w.Write(b)
	}
}

func (e *encoder) bucket(d *bucketData) {
	n := 0
	_ = walk(d.root, func(*node) error { n++; return nil })
	e.uvarint(d.seq)
	e.uvarint(uint64(n))
	_ = walk(d.root, func(n *node) error {
		e.bytes(n.key())
		if n.bkt != nil {
			e.uvarint(1)
			e.bucket(n.bkt)
		} else {
			e.uvarint(0)
			e.bytes(n.val())
		}
		return e.err
	})
}

// decodeFile returns the root bucket and the last bucket id it gave out.
func decodeFile(data []byte) (*bucketData, uint64, error) {
	if len(data) < len(magic)+1+4 || [8]byte(data[:8]) != magic {
		return nil, 0, errors.ErrInvalid
	}
	switch data[stateOff] {
	case stateClean:
	case stateInUse:
		return nil, 0, ErrUncleanShutdown
	default:
		return nil, 0, errors.ErrInvalid
	}
	body := data[stateOff+1 : len(data)-4]
	if crc32.Checksum(body, castagnoli) != binary.LittleEndian.Uint32(data[len(data)-4:]) {
		return nil, 0, errors.ErrChecksum
	}
	d := decoder{b: body}
	root := d.bucket()
	if d.err == nil && len(d.b) != 0 {
		d.err = errors.ErrInvalid
	}
	if d.err != nil {
		return nil, 0, d.err
	}
	return root, d.ids, nil
}

type decoder struct {
	b   []byte
	ids uint64
	err error
}

func (d *decoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.b)
	if n <= 0 {
		d.err = errors.ErrInvalid
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *decoder) bytes() []byte {
	n := d.uvarint()
	if d.err != nil {
		return nil
	}
	if n > uint64(len(d.b)) {
		d.err = errors.ErrInvalid
		return nil
	}
	v := d.b[:n:n]
	d.b = d.b[n:]
	return v
}

// bucket reads one bucket. Keys arrive in order, so each entry goes in with
// gen 0, owned by no transaction. Keys and values are copied out of the file's
// buffer so it can be freed.
func (d *decoder) bucket() *bucketData {
	b := &bucketData{seq: d.uvarint()}
	n := d.uvarint()
	var prev []byte
	for i := uint64(0); i < n && d.err == nil; i++ {
		k := d.bytes()
		if len(k) == 0 || (prev != nil && string(prev) >= string(k)) {
			d.err = errors.ErrInvalid
			break
		}
		switch d.uvarint() {
		case 0:
			v := d.bytes()
			kv := make([]byte, len(k)+len(v))
			copy(kv, k)
			copy(kv[len(k):], v)
			b.root = upsert(b.root, k, kv, uint32(len(k)), nil, prio(k), 0)
			prev = kv[:len(k)]
		case 1:
			kc := append([]byte(nil), k...)
			d.ids++
			id := d.ids
			child := d.bucket()
			child.id = id
			b.root = upsert(b.root, kc, kc, uint32(len(kc)), child, prio(kc), 0)
			prev = kc
		default:
			d.err = errors.ErrInvalid
		}
	}
	return b
}
