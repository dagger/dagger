package clientdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

// writeQueueSize bounds the number of telemetry batches retained by a client
// DB's writer. Producers block when it is full; telemetry is never dropped.
const writeQueueSize = 64

var errWriterClosed = errors.New("client DB writer is closed")

// WriteBatch performs one exporter's telemetry inserts. The duration it
// returns is the slowest individual statement in the batch.
type WriteBatch func(context.Context, *Queries) (time.Duration, error)

// WriteTiming decomposes one queued batch's time through the client DB writer.
// BeginWait is the time from submission until the batch starts inside a
// coalesced transaction, including bounded-queue backpressure and earlier
// queued batches. Commit is shared by every batch in the same transaction.
type WriteTiming struct {
	BeginWait time.Duration
	MaxStmt   time.Duration
	Commit    time.Duration
}

type writeRequest struct {
	ctx    context.Context
	start  time.Time
	write  WriteBatch
	result chan writeResponse
}

type writeResponse struct {
	timing WriteTiming
	err    error
}

// writeAgent is the sole acquirer of a client DB's write connection. It
// coalesces every currently queued batch into one transaction, using a
// savepoint to preserve per-batch rollback and error semantics.
type writeAgent struct {
	db      *sql.DB
	queries *Queries
	queue   chan *writeRequest
	done    chan struct{}

	// enqueueMu makes closing the queue safe against a producer blocked on
	// bounded-queue backpressure. The writer never holds it during SQLite work.
	enqueueMu sync.RWMutex
	closed    bool
}

func newWriteAgent(db *sql.DB, queries *Queries) *writeAgent {
	agent := &writeAgent{
		db:      db,
		queries: queries,
		queue:   make(chan *writeRequest, writeQueueSize),
		done:    make(chan struct{}),
	}
	go agent.run()
	return agent
}

func (agent *writeAgent) submit(ctx context.Context, write WriteBatch) (WriteTiming, error) {
	start := time.Now()
	req := &writeRequest{
		ctx:    ctx,
		start:  start,
		write:  write,
		result: make(chan writeResponse, 1),
	}

	agent.enqueueMu.RLock()
	if agent.closed {
		agent.enqueueMu.RUnlock()
		return WriteTiming{BeginWait: time.Since(start)}, errWriterClosed
	}
	select {
	case agent.queue <- req:
		agent.enqueueMu.RUnlock()
	case <-ctx.Done():
		agent.enqueueMu.RUnlock()
		return WriteTiming{BeginWait: time.Since(start)}, context.Cause(ctx)
	}

	// Once admitted, wait for this batch's commit watermark. Returning early
	// on ctx cancellation would let a successful export race the SSE reader.
	res := <-req.result
	return res.timing, res.err
}

func (agent *writeAgent) Close() {
	agent.enqueueMu.Lock()
	if !agent.closed {
		agent.closed = true
		close(agent.queue)
	}
	agent.enqueueMu.Unlock()
	<-agent.done
}

func (agent *writeAgent) run() {
	defer close(agent.done)

	for {
		first, ok := <-agent.queue
		if !ok {
			return
		}

		requests := []*writeRequest{first}
		queueClosed := false
	drain:
		for {
			select {
			case req, ok := <-agent.queue:
				if !ok {
					queueClosed = true
					break drain
				}
				requests = append(requests, req)
			default:
				break drain
			}
		}

		agent.writeRequests(requests)
		if queueClosed {
			return
		}
	}
}

func (agent *writeAgent) writeRequests(requests []*writeRequest) {
	pending := requests[:0]
	for _, req := range requests {
		if err := context.Cause(req.ctx); err != nil {
			req.result <- writeResponse{
				timing: WriteTiming{BeginWait: time.Since(req.start)},
				err:    err,
			}
			continue
		}
		pending = append(pending, req)
	}
	if len(pending) == 0 {
		return
	}

	tx, err := agent.db.BeginTx(context.Background(), nil)
	timings := make([]WriteTiming, len(pending))
	errs := make([]error, len(pending))
	if err != nil {
		for i, req := range pending {
			timings[i].BeginWait = time.Since(req.start)
		}
		agent.respond(pending, timings, errs, fmt.Errorf("begin tx: %w", err))
		return
	}

	q := agent.queries.WithTx(tx)
	var txErr error
	for i, req := range pending {
		timings[i].BeginWait = time.Since(req.start)
		if _, err := tx.ExecContext(context.Background(), "SAVEPOINT telemetry_batch"); err != nil {
			txErr = fmt.Errorf("savepoint telemetry batch: %w", err)
			break
		}

		timings[i].MaxStmt, errs[i] = req.write(context.WithoutCancel(req.ctx), q)
		if errs[i] != nil {
			if _, err := tx.ExecContext(context.Background(), "ROLLBACK TO SAVEPOINT telemetry_batch"); err != nil {
				txErr = fmt.Errorf("rollback telemetry batch: %w", err)
				break
			}
		}
		if _, err := tx.ExecContext(context.Background(), "RELEASE SAVEPOINT telemetry_batch"); err != nil {
			txErr = fmt.Errorf("release telemetry batch: %w", err)
			break
		}
	}

	if txErr != nil {
		txErr = errors.Join(txErr, tx.Rollback())
		agent.respond(pending, timings, errs, txErr)
		return
	}

	commitStart := time.Now()
	commitErr := tx.Commit()
	commitDuration := time.Since(commitStart)
	for i := range timings {
		timings[i].Commit = commitDuration
	}
	if commitErr != nil {
		commitErr = fmt.Errorf("commit telemetry batches: %w", commitErr)
	}
	agent.respond(pending, timings, errs, commitErr)
}

func (agent *writeAgent) respond(requests []*writeRequest, timings []WriteTiming, errs []error, transactionErr error) {
	for i, req := range requests {
		req.result <- writeResponse{
			timing: timings[i],
			err:    errors.Join(errs[i], transactionErr),
		}
	}
}
