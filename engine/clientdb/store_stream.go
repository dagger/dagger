package clientdb

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dagger/dagger/engine/slog"
)

// Each telemetry stream targets a 4 MiB in-memory window and blocks Append at
// 16x that budget (64 MiB by default) until the spiller makes room. The hard cap
// propagates backpressure into the bounded OTel BSP queues, which are the
// effective upstream bound; the wcprof completeness checksum accounts for
// dropped spans.
// A disk write that wedges without returning an error can therefore block
// appends indefinitely, matching SQLite's behavior under the same pathology.
const (
	telemetryTailBudget            int64 = 4 << 20
	telemetryTailHardCapMultiplier int64 = 16
)

var errStoreClosed = errors.New("telemetry store is closed")

type logStream[Row any] struct {
	mu         sync.Mutex
	codec      rowCodec[Row]
	nextID     int64
	tail       []Row
	tailSizes  []int64
	tailBase   int64
	tailBytes  int64
	budget     int64
	hardCap    int64
	spill      *spillFile[Row]
	onAppend   func([]Row)
	cond       *sync.Cond
	capWaiters int

	spillReq chan struct{}
	closeReq chan chan error
	closed   bool
	fatalErr error
}

func openLogStream[Row any](
	ctx context.Context,
	path string,
	codec rowCodec[Row],
	budget int64,
	onRecover func(Row),
	onAppend func([]Row),
) (*logStream[Row], error) {
	spill, err := openSpillFile(ctx, path, codec, onRecover)
	if err != nil {
		return nil, err
	}
	if spill.lastID == maxStreamID {
		return nil, errors.Join(fmt.Errorf("telemetry row ID space exhausted"), spill.close())
	}
	if budget <= 0 {
		budget = telemetryTailBudget
	}
	hardCap := maxStreamID
	if budget <= maxStreamID/telemetryTailHardCapMultiplier {
		hardCap = budget * telemetryTailHardCapMultiplier
	}
	stream := &logStream[Row]{
		codec:    codec,
		nextID:   spill.lastID + 1,
		tailBase: spill.lastID + 1,
		budget:   budget,
		hardCap:  hardCap,
		spill:    spill,
		onAppend: onAppend,
		spillReq: make(chan struct{}, 1),
		closeReq: make(chan chan error),
	}
	stream.cond = sync.NewCond(&stream.mu)
	go stream.runSpiller()
	return stream, nil
}

// Append assigns increasing IDs and publishes rows under the stream mutex.
// Once it returns, every reader can observe every appended row. Blob slices in
// rows are immutable after Append; tail reads intentionally share their bytes.
func (s *logStream[Row]) Append(rows []Row) (int64, error) {
	sizes := make([]int64, len(rows))
	var totalSize int64
	for i, row := range rows {
		sizes[i] = s.codec.size(row)
		if sizes[i] > maxStreamID-totalSize {
			totalSize = maxStreamID
		} else {
			totalSize += sizes[i]
		}
	}

	s.mu.Lock()
	if err := s.stateErrLocked(); err != nil {
		s.mu.Unlock()
		return 0, err
	}
	if len(rows) == 0 {
		last := s.nextID - 1
		s.mu.Unlock()
		return last, nil
	}

	oversized := totalSize > s.hardCap
	for (!oversized && s.tailBytes > s.hardCap-totalSize) || (oversized && s.tailBytes > 0) {
		s.capWaiters++
		s.requestSpill()
		s.cond.Wait()
		s.capWaiters--
		if err := s.stateErrLocked(); err != nil {
			s.mu.Unlock()
			return 0, err
		}
	}
	if int64(len(rows)) > maxStreamID-s.nextID {
		s.mu.Unlock()
		return 0, fmt.Errorf("telemetry row ID space exhausted")
	}
	for i := range rows {
		s.codec.setID(&rows[i], s.nextID)
		s.nextID++
	}
	if s.onAppend != nil {
		s.onAppend(rows)
	}
	s.tail = append(s.tail, rows...)
	s.tailSizes = append(s.tailSizes, sizes...)
	if totalSize > maxStreamID-s.tailBytes {
		s.tailBytes = maxStreamID
	} else {
		s.tailBytes += totalSize
	}
	last := s.nextID - 1
	needSpill := s.tailBytes > s.budget
	if oversized {
		// The caller already owns an oversized batch's blobs, so admitting it
		// into an empty tail adds only row headers. Keep this Append blocked so
		// another batch cannot accumulate until the spiller is below the cap.
		s.capWaiters++
		s.requestSpill()
		for s.tailBytes > s.hardCap {
			s.cond.Wait()
			if err := s.stateErrLocked(); err != nil {
				s.capWaiters--
				s.mu.Unlock()
				return 0, err
			}
		}
		s.capWaiters--
	}
	s.mu.Unlock()

	if needSpill {
		s.requestSpill()
	}
	return last, nil
}

func (s *logStream[Row]) Since(ctx context.Context, id int64, limit int) ([]Row, error) {
	if limit <= 0 {
		return nil, nil
	}
	if id < 0 {
		id = 0
	}
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	default:
	}

	s.mu.Lock()
	if id >= s.tailBase-1 {
		start := id - s.tailBase + 1
		if start < 0 {
			start = 0
		}
		if start >= int64(len(s.tail)) {
			s.mu.Unlock()
			return nil, nil
		}
		count := min(limit, len(s.tail)-int(start))
		rows := make([]Row, count)
		copy(rows, s.tail[int(start):int(start)+count])
		s.mu.Unlock()
		return rows, nil
	}
	tailBase := s.tailBase
	s.mu.Unlock()

	fileRows := tailBase - id - 1
	if fileRows < int64(limit) {
		limit = int(fileRows)
	}
	return s.spill.readSince(ctx, id, limit)
}

func (s *logStream[Row]) readID(ctx context.Context, id int64) (Row, bool, error) {
	var zero Row
	if id <= 0 {
		return zero, false, nil
	}
	s.mu.Lock()
	if id >= s.tailBase {
		index := id - s.tailBase
		if index >= int64(len(s.tail)) {
			s.mu.Unlock()
			return zero, false, nil
		}
		row := s.tail[index]
		s.mu.Unlock()
		return row, true, nil
	}
	s.mu.Unlock()
	return s.spill.readID(ctx, id)
}

func (s *logStream[Row]) requestSpill() {
	select {
	case s.spillReq <- struct{}{}:
	default:
	}
}

func (s *logStream[Row]) stateErrLocked() error {
	if s.closed {
		return errStoreClosed
	}
	return s.fatalErr
}

func (s *logStream[Row]) runSpiller() {
	for {
		select {
		case <-s.spillReq:
			for {
				spilled, err := s.spillOnce(false)
				if err != nil || !spilled {
					break
				}
			}
		case response := <-s.closeReq:
			var err error
			for {
				spilled, spillErr := s.spillOnce(true)
				err = errors.Join(err, spillErr)
				if spillErr != nil || !spilled {
					break
				}
			}
			err = errors.Join(err, s.spill.close())
			response <- err
			return
		}
	}
}

func (s *logStream[Row]) spillOnce(force bool) (bool, error) {
	s.mu.Lock()
	if s.fatalErr != nil {
		err := s.fatalErr
		s.mu.Unlock()
		return false, err
	}
	underPressure := s.capWaiters > 0
	if len(s.tail) == 0 || (!force && !underPressure && s.tailBytes <= s.budget) {
		s.mu.Unlock()
		return false, nil
	}

	n := len(s.tail)
	if !force && !underPressure {
		// Spill down to half the target to amortize wakeups and file flushes.
		remaining := s.tailBytes
		n = 0
		for n < len(s.tail) && remaining > s.budget/2 {
			remaining -= s.tailSizes[n]
			n++
		}
	}
	rows := append([]Row(nil), s.tail[:n]...)
	var spilledBytes int64
	for _, size := range s.tailSizes[:n] {
		spilledBytes += size
	}
	s.mu.Unlock()

	if err := s.spill.append(rows); err != nil {
		s.setFatal(err)
		return false, err
	}

	s.mu.Lock()
	var zero Row
	for i := range n {
		s.tail[i] = zero
	}
	s.tail = s.tail[n:]
	s.tailSizes = s.tailSizes[n:]
	s.tailBytes -= spilledBytes
	if len(s.tail) > 0 {
		s.tailBase = s.codec.getID(s.tail[0])
	} else {
		s.tailBase = s.nextID
	}
	s.cond.Broadcast()
	s.mu.Unlock()
	return true, nil
}

func (s *logStream[Row]) setFatal(err error) {
	s.mu.Lock()
	if s.fatalErr == nil {
		s.fatalErr = fmt.Errorf("spill telemetry stream %s: %w", s.spill.file.Name(), err)
		slog.Error("client telemetry spill failed", "path", s.spill.file.Name(), "error", err)
		s.cond.Broadcast()
	}
	s.mu.Unlock()
}

func (s *logStream[Row]) close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errStoreClosed
	}
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()

	response := make(chan error, 1)
	s.closeReq <- response
	return <-response
}

const maxStreamID = int64(^uint64(0) >> 1)
