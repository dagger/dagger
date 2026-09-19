package dagql

import (
	"context"
	"fmt"
	"slices"

	"github.com/opencontainers/go-digest"
	"golang.org/x/sync/errgroup"
)

// HasPartHost binds a typed adapter to the stable owning row. Binding never
// creates a gate and decoding another representation keeps the same host.
type HasPartHost interface{ BindPartHost(*PartHost) }
type HasPartHostBinding interface{ PartHostBinding() *PartHost }
type PartHost struct {
	cache *Cache
	row   *sharedResult
	path  PersistedRefPath
}

func (c *Cache) bindPartHost(row *sharedResult, result AnyResult) {
	if row.inlineBorrow != nil {
		return
	}
	if value, ok := UnwrapAs[HasPartHost](result); ok {
		value.BindPartHost(c.partHostFor(row))
	}
}
func (host *PartHost) Evaluate(ctx context.Context, parts ...PartKey) error {
	if len(host.path) == 0 {
		return host.cache.EvaluateParts(ctx, Result[Typed]{shared: host.row}, parts...)
	}
	root := Result[Typed]{shared: host.row}
	value, err := inlineValueAt(root, host.row.loadResultCall(), host.path)
	if err != nil {
		return err
	}
	for {
		if host.cache.usesPartAcquisition(value, host.row) {
			err := host.cache.evaluateAcquiredScope(ctx, root, host.row, host.path, parts)
			if partCanReselect(err) {
				continue
			}
			return err
		}
		var groups []LazyGroupKey
		refined, isRefined := UnwrapAs[HasLazyEvaluationParts](value)
		if isRefined {
			groups, err = refined.ResolveLazyEvalGroups(ctx, value, parts)
			if err != nil {
				return err
			}
		} else {
			groups = []LazyGroupKey{LazyGroupWhole}
		}
		eg, groupCtx := errgroup.WithContext(ctx)
		for _, group := range groups {
			eg.Go(func() error {
				return host.cache.RunLazyTask(groupCtx, root, lazyEvaluationTaskKey(LazyGroupAddress{OutputPath: host.path, Group: group}), LazyTaskSpec{Body: func(ctx context.Context) error {
					var body LazyEvalFunc
					if isRefined {
						body = refined.LazyEvalFuncForGroup(group)
					} else {
						body = lazyEvalFuncOfResult(value)
					}
					if body != nil {
						return body(ctx)
					}
					return nil
				}})
			})
		}
		err = eg.Wait()
		if !partCanReselect(err) {
			return err
		}
	}
}
func (host *PartHost) Admitted(ctx context.Context) bool {
	token := PartTaskFromContext(ctx)
	return token != nil && token.row == host.row && token.active.Load()
}

// RunNative enters the gate before the body takes any core latch. The raw
// callback remains the only owner of native completion and consumption.
func (host *PartHost) RunNative(ctx context.Context, group LazyGroupKey, parts []PartKey, body func(context.Context) error) error {
	if !host.Admitted(ctx) {
		return host.Evaluate(ctx, parts...)
	}
	token := PartTaskFromContext(ctx)
	c := host.cache
	c.egraphMu.Lock()
	gate := host.row.partGate.loadOrCreate()
	gate.mu.Lock()
	groupAddress := LazyGroupAddress{OutputPath: host.path, Group: group}
	key := lazyGroupAddressKey(groupAddress)
	if gate.managed {
		gate.mu.Unlock()
		c.egraphMu.Unlock()
		return partRefused("native: gate is managed")
	}
	writes := make([]PersistedPartAddress, len(parts))
	for i, part := range parts {
		writes[i] = PersistedPartAddress{OutputPath: slices.Clone(host.path), Part: part}
	}
	for _, writer := range gate.writers {
		if containsPart(writes, writer.address) {
			gate.mu.Unlock()
			c.egraphMu.Unlock()
			return ErrLazyTaskBusy
		}
	}
	gate.groups[key] = &partLazyEvaluationState{phase: LazyEvaluationRunning, task: token, writeSet: writes}
	gate.revision++
	gate.mu.Unlock()
	c.egraphMu.Unlock()
	err := body(ctx)
	// Native core callbacks have released their latches before this publication.
	c.egraphMu.Lock()
	gate.mu.Lock()
	current := gate.groups[key]
	if current.task == token {
		if err == nil {
			current.phase = LazyEvaluationEvaluated
			installed := &InstalledOutputs{}
			for _, address := range writes {
				output, _ := partAddressKey(address)
				gate.outputs[output] = partOutputState{phase: PartOutputInstalled, task: token, installation: token.generation}
				installed.outputs = append(installed.outputs, installedPartOutput{address: clonePartAddress(address), installation: token.generation})
			}
			token.installed.Store(installed)
		} else {
			current.phase = LazyEvaluationOpen
		}
		gate.revision++
	}
	gate.mu.Unlock()
	c.egraphMu.Unlock()
	return err
}

// SetContentDigestAfterEvaluation defers output equivalence until this native
// attempt has materialized and settled its output. Inline values do not have an
// independent result identity, so they cannot publish a digest on their owner.
func (host *PartHost) SetContentDigestAfterEvaluation(ctx context.Context, contentDigest digest.Digest, labels ...string) error {
	if len(host.path) != 0 {
		return nil
	}
	if !host.Admitted(ctx) {
		return fmt.Errorf("defer content digest: no admitted native evaluation")
	}
	return PartTaskFromContext(ctx).SetContentDigestAfterEvaluation(contentDigest, labels...)
}

// Only the owning native attempt may advance installed output to complete,
// after owner sync and operation-lease cleanup have both succeeded.
func (c *Cache) completeNativePartTask(ctx context.Context, task *PartTaskToken) error {
	installed := task.installed.Load()
	if installed == nil {
		return nil
	}
	for _, output := range installed.outputs {
		if err := c.settlePart(ctx, task.row, output.address, task, output.installation); err != nil {
			return err
		}
	}
	return c.teachTaskContentIdentity(ctx, task)
}

// DecodeContext borrows this held owner's exact recorded identity and server.
func (host *PartHost) DecodeContext(ctx context.Context) *PersistDecodeContext {
	return host.cache.partDecodeContext(ctx, host.row, PersistedRecord{Call: host.row.loadResultCall(), SnapshotLinks: host.row.loadSnapshotOwnerLinks()}).atPath(host.path)
}

func (host *PartHost) Managed() bool { return host != nil && host.row.partGate.active.Load() }

func (c *Cache) partHostFor(row *sharedResult) *PartHost {
	row.partGate.hostOnce.Do(func() { row.partGate.host = PartHost{cache: c, row: row} })
	return &row.partGate.host
}

func (host *PartHost) at(path PersistedRefPath) *PartHost {
	return &PartHost{cache: host.cache, row: host.row, path: append(PersistedRefPath(nil), path...)}
}

func (c *Cache) attachInlineHosts(row *sharedResult, res AnyResult) error {
	if row.inlineBorrow != nil {
		return nil
	}
	if _, ok := UnwrapAs[Enumerable](res); !ok {
		return nil
	}
	var cleanup OnReleaseFunc
	err := walkInlineValues(res, row.loadResultCall(), nil, true, func(value AnyResult, path PersistedRefPath) error {
		if len(path) == 0 {
			return nil
		}
		host := c.partHostFor(row).at(path)
		if target, ok := UnwrapAs[HasPartHost](value); ok {
			target.BindPartHost(host)
		}
		if release, ok := UnwrapAs[OnReleaser](value); ok {
			cleanup = joinOnRelease(cleanup, release.OnRelease)
		}
		return nil
	})
	row.payloadMu.Lock()
	row.onRelease = joinOnRelease(row.onRelease, cleanup)
	row.payloadMu.Unlock()
	return err
}

func (c *Cache) validateInlineBorrow(host *PartHost, req *CallRequest, val AnyResult) error {
	if host == nil || len(host.path) == 0 || host.cache != c || req.Receiver == nil {
		return fmt.Errorf("invalid inline borrow")
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	if c.resultsByID[host.row.id] != host.row {
		return fmt.Errorf("inline borrow owner disappeared")
	}
	parent := c.resultsByID[sharedResultID(req.Receiver.ResultID)]
	if parent == nil || (parent != host.row && (parent.inlineBorrow == nil || parent.inlineBorrow.row != host.row)) {
		return fmt.Errorf("inline borrow has no owner receiver")
	}
	if bound, ok := UnwrapAs[HasPartHostBinding](val); ok {
		actual := bound.PartHostBinding()
		if actual == nil || actual.row != host.row || !slices.Equal(actual.path, host.path) {
			return fmt.Errorf("inline borrow attachment mismatch")
		}
	}
	return nil
}
