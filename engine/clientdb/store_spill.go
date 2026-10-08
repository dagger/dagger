package clientdb

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
)

const (
	storeFormatVersion byte  = 1
	sparseIndexStride  int64 = 256
	spillBufferSize          = 64 << 10
)

type spillIndexEntry struct {
	id     int64
	offset int64
}

// spillFile has a single writer (the stream's spiller) and any number of
// concurrent readers. committed and index publish only complete, flushed
// frames, so a SectionReader bounded by committed can never see a partial row.
type spillFile[Row any] struct {
	file   *os.File
	codec  rowCodec[Row]
	writer *bufio.Writer
	// testWriteHook is a fault-injection seam. Tests install it only while the
	// single spiller is idle; production always leaves it nil.
	testWriteHook func([]byte) (int, error)
	testSyncHook  func() error

	// The fields below are owned by the single writer.
	writeOffset int64
	rowCount    int64
	lastID      int64

	mu              sync.RWMutex
	committed       int64
	committedLastID int64
	index           []spillIndexEntry
}

func openSpillFile[Row any](
	ctx context.Context,
	path string,
	codec rowCodec[Row],
	onRecover func(Row),
) (_ *spillFile[Row], rerr error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, file.Close())
		}
	}()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat spill file: %w", err)
	}
	size := info.Size()
	if size == 0 {
		if _, err := file.Write([]byte{storeFormatVersion}); err != nil {
			return nil, fmt.Errorf("write spill header: %w", err)
		}
		size = 1
	} else {
		var header [1]byte
		if _, err := file.ReadAt(header[:], 0); err != nil {
			return nil, fmt.Errorf("read spill header: %w", err)
		}
		if header[0] != storeFormatVersion {
			return nil, fmt.Errorf("unsupported telemetry store format version %d", header[0])
		}
	}

	spill := &spillFile[Row]{
		file:      file,
		codec:     codec,
		committed: size,
	}
	if err := spill.recover(ctx, size, onRecover); err != nil {
		return nil, err
	}
	if _, err := file.Seek(spill.writeOffset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek spill writer: %w", err)
	}
	spill.writer = bufio.NewWriterSize(spillWriterFunc(spill.write), spillBufferSize)
	return spill, nil
}

type spillWriterFunc func([]byte) (int, error)

func (write spillWriterFunc) Write(buf []byte) (int, error) {
	return write(buf)
}

func (s *spillFile[Row]) write(buf []byte) (int, error) {
	if s.testWriteHook != nil {
		return s.testWriteHook(buf)
	}
	return s.file.Write(buf)
}

func (s *spillFile[Row]) recover(ctx context.Context, size int64, onRecover func(Row)) error {
	scanner := newFrameScanner(s.file, 1, size)
	for {
		frameOffset := scanner.offset
		payload, err := scanner.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			// A process exit can leave only the final frame incomplete. The store
			// has no crash-durability contract, so recover the complete prefix.
			if err := s.file.Truncate(frameOffset); err != nil {
				return fmt.Errorf("truncate incomplete spill frame at %d: %w", frameOffset, err)
			}
			size = frameOffset
			break
		}
		if err != nil {
			return fmt.Errorf("read spill frame at %d: %w", frameOffset, err)
		}
		row, err := s.codec.decode(payload)
		if err != nil {
			return fmt.Errorf("decode spill frame at %d: %w", frameOffset, err)
		}
		id := s.codec.getID(row)
		if id <= s.lastID {
			return fmt.Errorf("spill row ID %d is not greater than previous ID %d", id, s.lastID)
		}
		if s.rowCount%sparseIndexStride == 0 {
			s.index = append(s.index, spillIndexEntry{id: id, offset: frameOffset})
		}
		s.rowCount++
		s.lastID = id
		if onRecover != nil {
			onRecover(row)
		}
		if s.rowCount%sparseIndexStride == 0 {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			default:
			}
		}
	}

	s.writeOffset = size
	s.committed = size
	s.committedLastID = s.lastID
	return nil
}

func (s *spillFile[Row]) append(rows []Row) error {
	if len(rows) == 0 {
		return nil
	}

	previousID := s.lastID
	for _, row := range rows {
		id := s.codec.getID(row)
		if id <= previousID {
			return fmt.Errorf("append spill row ID %d after %d", id, previousID)
		}
		previousID = id
	}

	start := s.writeOffset
	offset := start
	rowCount := s.rowCount
	var pendingIndex []spillIndexEntry
	for _, row := range rows {
		payload := s.codec.encode(row)
		var prefix [binary.MaxVarintLen64]byte
		prefixLen := binary.PutUvarint(prefix[:], uint64(len(payload)))
		if rowCount%sparseIndexStride == 0 {
			pendingIndex = append(pendingIndex, spillIndexEntry{
				id:     s.codec.getID(row),
				offset: offset,
			})
		}
		if _, err := s.writer.Write(prefix[:prefixLen]); err != nil {
			return s.rollback(start, fmt.Errorf("write spill frame length: %w", err))
		}
		if _, err := s.writer.Write(payload); err != nil {
			return s.rollback(start, fmt.Errorf("write spill frame: %w", err))
		}
		offset += int64(prefixLen + len(payload))
		rowCount++
	}
	if err := s.writer.Flush(); err != nil {
		return s.rollback(start, fmt.Errorf("flush spill frames: %w", err))
	}

	s.writeOffset = offset
	s.rowCount = rowCount
	s.lastID = previousID
	s.mu.Lock()
	s.index = append(s.index, pendingIndex...)
	s.committed = offset
	s.committedLastID = previousID
	s.mu.Unlock()
	return nil
}

func (s *spillFile[Row]) rollback(offset int64, writeErr error) error {
	// Reset discards any bytes still buffered after the failed write.
	s.writer.Reset(spillWriterFunc(s.write))
	truncateErr := s.file.Truncate(offset)
	_, seekErr := s.file.Seek(offset, io.SeekStart)
	s.writer.Reset(spillWriterFunc(s.write))
	return errors.Join(writeErr, truncateErr, seekErr)
}

func (s *spillFile[Row]) readSince(ctx context.Context, id int64, limit int) ([]Row, error) {
	if limit <= 0 {
		return nil, nil
	}
	offset, committed, ok := s.readBounds(id)
	if !ok {
		return nil, nil
	}

	scanner := newFrameScanner(s.file, offset, committed)
	rows := make([]Row, 0, limit)
	for len(rows) < limit {
		payload, err := scanner.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read committed spill frame: %w", err)
		}
		row, err := s.codec.decode(payload)
		if err != nil {
			return nil, fmt.Errorf("decode committed spill frame: %w", err)
		}
		if s.codec.getID(row) > id {
			rows = append(rows, row)
		}
		if len(rows)%int(sparseIndexStride) == 0 {
			select {
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			default:
			}
		}
	}
	return rows, nil
}

func (s *spillFile[Row]) readID(ctx context.Context, id int64) (Row, bool, error) {
	var (
		row   Row
		found bool
	)
	err := s.readIDs(ctx, []int64{id}, func(r Row) {
		row, found = r, true
	})
	return row, found, err
}

// readIDs reads the committed rows with the given IDs, which must be
// ascending, calling fn for each one present. One scanner and one payload
// buffer serve the whole batch: it seeks through the sparse index only when
// the next ID lies outside the stretch it is already positioned in, and the
// frames it passes on the way to a wanted row are skipped by their ID alone,
// without decoding them. A scoped load reads its rows in exactly this shape
// -- thousands of ascending IDs, many within a stride of each other -- where
// a fresh scanner per row re-read and decoded up to sparseIndexStride frames
// (and allocated a fresh read buffer) for every single row.
func (s *spillFile[Row]) readIDs(ctx context.Context, ids []int64, fn func(Row)) error {
	if len(ids) == 0 {
		return nil
	}
	s.mu.RLock()
	index := s.index
	committed := s.committed
	committedLastID := s.committedLastID
	s.mu.RUnlock()

	var (
		scanner *frameScanner
		scanned int64 // ID of the last frame scanner read
		buf     []byte
	)
	defer func() { scanner.release() }()
	for i, id := range ids {
		if i%int(sparseIndexStride) == 0 {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			default:
			}
		}
		if len(index) == 0 || id <= 0 || id > committedLastID {
			continue
		}
		// The final sparse entry at or before id: at most sparseIndexStride
		// frames lie between it and the row.
		at := sort.Search(len(index), func(i int) bool { return index[i].id > id }) - 1
		if at < 0 {
			at = 0
		}
		entry := index[at]
		if scanner == nil || scanned >= id || scanner.offset < entry.offset {
			// Not positioned between the entry and the row: seek. Reading on
			// from where the scanner stands is only cheaper when it already
			// sits past the entry.
			if scanner == nil {
				scanner = newFrameScanner(s.file, entry.offset, committed)
			} else {
				scanner.reset(s.file, entry.offset, committed)
			}
			scanned = 0
		}
		for {
			var err error
			buf, err = scanner.nextInto(buf[:0])
			if errors.Is(err, io.EOF) {
				// Past the last committed frame: this ID, and every later
				// one, is absent.
				return nil
			}
			if err != nil {
				return fmt.Errorf("read committed spill frame: %w", err)
			}
			rowID, err := frameRowID(buf)
			if err != nil {
				return fmt.Errorf("decode committed spill frame: %w", err)
			}
			scanned = rowID
			if rowID < id {
				continue
			}
			if rowID == id {
				row, err := s.codec.decode(buf)
				if err != nil {
					return fmt.Errorf("decode committed spill frame: %w", err)
				}
				fn(row)
			}
			// rowID > id: the row is absent, and the frame just read may be
			// the next one wanted; scanned >= the next ID forces a re-seek.
			break
		}
	}
	return nil
}

// frameRowID reads a spill frame's row ID without decoding the row: every
// row codec encodes the ID first, as a varint (see encodeSpan, encodeLog and
// metricCodec).
func frameRowID(payload []byte) (int64, error) {
	id, n := binary.Varint(payload)
	if n <= 0 {
		return 0, fmt.Errorf("decode row ID: malformed varint")
	}
	return id, nil
}

func (s *spillFile[Row]) readBounds(id int64) (offset, committed int64, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.index) == 0 || id >= s.committedLastID {
		return 0, 0, false
	}

	// Start at the final sparse entry whose first ID can still precede the
	// requested row. At most sparseIndexStride frames are skipped afterward.
	indexAt := sort.Search(len(s.index), func(i int) bool {
		return s.index[i].id > id
	})
	if indexAt > 0 {
		indexAt--
	}
	return s.index[indexAt].offset, s.committed, true
}

func (s *spillFile[Row]) close() error {
	return errors.Join(s.writer.Flush(), s.file.Close())
}

type frameScanner struct {
	reader *bufio.Reader
	offset int64
	end    int64
}

func newFrameScanner(file *os.File, offset, end int64) *frameScanner {
	reader, _ := frameReaderPool.Get().(*bufio.Reader)
	if reader == nil {
		reader = bufio.NewReaderSize(nil, spillBufferSize)
	}
	reader.Reset(io.NewSectionReader(file, offset, end-offset))
	return &frameScanner{
		reader: reader,
		offset: offset,
		end:    end,
	}
}

// frameReaderPool recycles scanners' read buffers: point reads (readID) each
// need one, and allocating spillBufferSize per row read is a GC storm on a
// path that reads hundreds of thousands of rows.
var frameReaderPool sync.Pool

// release returns the scanner's read buffer to the pool. The scanner must
// not be used afterwards.
func (s *frameScanner) release() {
	if s == nil || s.reader == nil {
		return
	}
	s.reader.Reset(nil)
	frameReaderPool.Put(s.reader)
	s.reader = nil
}

// reset repositions the scanner at offset, keeping its read buffer.
func (s *frameScanner) reset(file *os.File, offset, end int64) {
	s.reader.Reset(io.NewSectionReader(file, offset, end-offset))
	s.offset = offset
	s.end = end
}

func (s *frameScanner) ReadByte() (byte, error) {
	if s.offset == s.end {
		return 0, io.EOF
	}
	b, err := s.reader.ReadByte()
	if err != nil {
		return 0, err
	}
	s.offset++
	return b, nil
}

func (s *frameScanner) next() ([]byte, error) {
	return s.nextInto(nil)
}

// nextInto reads the next frame's payload into buf's backing array when it
// fits, allocating otherwise. The returned slice aliases buf: callers that
// keep a payload past the next read must copy it (the row codecs' decoders
// copy every field).
func (s *frameScanner) nextInto(buf []byte) ([]byte, error) {
	if s.offset == s.end {
		return nil, io.EOF
	}
	length, err := binary.ReadUvarint(s)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	if length > uint64(maxInt) {
		return nil, fmt.Errorf("spill frame length %d overflows int", length)
	}
	if length > uint64(s.end-s.offset) {
		return nil, io.ErrUnexpectedEOF
	}
	var payload []byte
	if uint64(cap(buf)) >= length {
		payload = buf[:int(length)]
	} else {
		payload = make([]byte, int(length))
	}
	if _, err := io.ReadFull(s.reader, payload); err != nil {
		return nil, err
	}
	s.offset += int64(length)
	return payload, nil
}
