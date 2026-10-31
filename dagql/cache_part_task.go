package dagql

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dagger/dagger/engine/wcprof"
	"github.com/opencontainers/go-digest"
)

// LazyTaskSpec supplies a coordination body, independent of the value's native
// lazy callback. A successful publication retains the continuation on failure.
type LazyTaskSpec struct {
	Body           func(context.Context) error
	AfterOwnerSync func(context.Context) error
	Settled        func(context.Context) error
	OwnerSyncReady <-chan struct{}
	NoJoin         bool
}

var ErrLazyTaskBusy = errors.New("lazy task already active or awaiting bookkeeping")

type PartTaskToken struct {
	row             *sharedResult
	key             LazyGroupKey
	generation      uint64
	active          atomic.Bool
	installed       atomic.Pointer[InstalledOutputs]
	contentIdentity *lazyContentIdentity
	ownerSync       chan struct{}
	ownerSyncOnce   sync.Once
	settled         atomic.Bool
	// alsoFilled are root parts of the row the body filled beyond its
	// installed outputs, such as a container's delegated parts copied by a
	// sweep; the owner lease sync after the body covers them too.
	alsoFilledMu sync.Mutex
	alsoFilled   []PartKey
}

// AlsoFilled records root parts of the task's row that its body filled
// beyond the outputs it installs, so the owner lease sync after the body
// reconciles them too. A nil token, outside any task, records nothing.
func (t *PartTaskToken) AlsoFilled(parts ...PartKey) {
	if t == nil {
		return
	}
	t.alsoFilledMu.Lock()
	defer t.alsoFilledMu.Unlock()
	for _, part := range parts {
		if !slices.Contains(t.alsoFilled, part) {
			t.alsoFilled = append(t.alsoFilled, part)
		}
	}
}

func (t *PartTaskToken) alsoFilledParts() []PartKey {
	if t == nil {
		return nil
	}
	t.alsoFilledMu.Lock()
	defer t.alsoFilledMu.Unlock()
	return slices.Clone(t.alsoFilled)
}

// lazyContentIdentity is published only after successful materialization and its
// ownership bookkeeping succeed. The installation token retains it for retries.
type lazyContentIdentity struct {
	digest digest.Digest
	labels []string
}

// SetContentDigestAfterEvaluation schedules content equivalence for a successful
// output installation. Failed bodies and unfinished cleanup never publish it.
func (t *PartTaskToken) SetContentDigestAfterEvaluation(contentDigest digest.Digest, labels ...string) error {
	if t == nil || !t.active.Load() {
		return fmt.Errorf("defer content digest: no active evaluation")
	}
	if contentDigest == "" {
		return fmt.Errorf("defer content digest: empty digest")
	}
	t.contentIdentity = &lazyContentIdentity{digest: contentDigest, labels: slices.Clone(labels)}
	return nil
}

func (c *Cache) teachTaskContentIdentity(ctx context.Context, task *PartTaskToken) error {
	if task.installed.Load() == nil {
		return nil
	}
	if identity := task.contentIdentity; identity != nil {
		return c.TeachContentDigest(ctx, Result[Typed]{shared: task.row}, identity.digest, identity.labels...)
	}
	return nil
}

// InstalledOutputs is immutable after publication. Installation identity is
// distinct from the generation executing a later bookkeeping-only retry.
type InstalledOutputs struct {
	outputs     []installedPartOutput
	protections []*partProtection
	beforeSync  []*partCleanup
}
type installedPartOutput struct {
	address      PersistedPartAddress
	installation uint64
}
type partTaskContextKey struct{}

func PartTaskFromContext(ctx context.Context) *PartTaskToken {
	token, _ := ctx.Value(partTaskContextKey{}).(*PartTaskToken)
	return token
}

type lazyTaskContinuation struct {
	spec                     LazyTaskSpec
	token                    *PartTaskToken
	synced, cleaned, settled bool
}

// finish is the bookkeeping after the task's body. When profiled, each of its
// steps that has work is a step of its own (lazyBody.*), and a wait for the
// owner sync to be ready is a lazy wait: one op or wait per step a body runs,
// none per part.
func (t *lazyTaskContinuation) finish(ctx context.Context, c *Cache, row *sharedResult) error {
	if installed := t.token.installed.Load(); installed != nil && len(installed.beforeSync) > 0 {
		stepCtx, step := BeginStep(ctx, "lazyBody.cleanup")
		for _, cleanup := range installed.beforeSync {
			if err := cleanup.release(stepCtx); err != nil {
				step.End(err)
				return err
			}
		}
		step.End(nil)
	}
	if t.spec.OwnerSyncReady != nil {
		select {
		case <-t.spec.OwnerSyncReady:
		case <-t.token.ownerSync:
		case <-ctx.Done():
			return context.Cause(ctx)
		default:
			wait := wcprof.BeginWaitIdent(ctx, "dagql.lazyBody.ownerSyncReady", wcprof.WaitReasonLazy)
			select {
			case <-t.spec.OwnerSyncReady:
			case <-t.token.ownerSync:
			case <-ctx.Done():
				wait.End()
				return context.Cause(ctx)
			}
			wait.End()
		}
	}
	if !t.synced {
		c.recordPartFixture(row, PersistedPartAddress{}, "owner-sync")
		stepCtx, step := BeginStep(ctx, "lazyBody.ownerSync")
		err := c.syncTaskPartLeases(stepCtx, row, t.token)
		step.End(err)
		if err != nil {
			return err
		}
		t.synced = true
	}
	if !t.cleaned {
		if installed := t.token.installed.Load(); installed != nil && len(installed.protections) > 0 {
			stepCtx, step := BeginStep(ctx, "lazyBody.releaseProtections")
			for _, protection := range installed.protections {
				if err := protection.release(stepCtx); err != nil {
					step.End(err)
					return err
				}
			}
			step.End(nil)
		}
		if t.spec.AfterOwnerSync != nil {
			stepCtx, step := BeginStep(ctx, "lazyBody.afterOwnerSync")
			err := t.spec.AfterOwnerSync(stepCtx)
			step.End(err)
			if err != nil {
				return err
			}
		}
		t.cleaned = true
	}
	if !t.settled {
		installed := t.token.installed.Load()
		if (installed != nil && len(installed.outputs) > 0) || t.spec.Settled != nil {
			stepCtx, step := BeginStep(ctx, "lazyBody.settle")
			err := t.settle(stepCtx, c, row, installed)
			step.End(err)
			if err != nil {
				return err
			}
		}
		t.settled = true
	}
	return nil
}

// settle settles the parts the task installed, then runs its Settled.
func (t *lazyTaskContinuation) settle(ctx context.Context, c *Cache, row *sharedResult, installed *InstalledOutputs) error {
	if installed != nil {
		for _, output := range installed.outputs {
			if err := c.settlePart(ctx, row, output.address, t.token, output.installation); err != nil {
				return err
			}
		}
	}
	if t.spec.Settled != nil {
		return t.spec.Settled(ctx)
	}
	return nil
}

func (c *Cache) RunLazyTask(ctx context.Context, receiver AnyResult, key LazyGroupKey, spec LazyTaskSpec) (rerr error) {
	op, row, err := c.beginEvaluateOne(ctx, receiver)
	if err != nil {
		return err
	}
	if row == nil {
		return fmt.Errorf("lazy task: missing receiver")
	}
	defer func() {
		if op.finish(rerr == nil) {
			rerr = errors.Join(rerr, ErrCacheSessionReleased)
		}
	}()
	if key == LazyGroupWhole {
		return fmt.Errorf("lazy task: synthetic task requires a named key")
	}
	c.egraphMu.Lock()
	if c.resultsByID[row.id] != row {
		c.egraphMu.Unlock()
		return fmt.Errorf("lazy task: unregistered receiver")
	}
	c.incrementIncomingOwnershipLocked(ctx, row)
	c.egraphMu.Unlock()
	defer func() { rerr = errors.Join(rerr, c.releasePartRow(context.WithoutCancel(ctx), row)) }()
	if err := c.runLazyTask(ctx, receiver, row, key, nil, &spec); err != nil {
		return err
	}
	if op.sessionID != "" {
		c.egraphMu.RLock()
		allowed := c.sessionSatisfiesResourceRequirementsLocked(op.sessionID, row)
		c.egraphMu.RUnlock()
		if !allowed {
			return fmt.Errorf("part task: session no longer satisfies installed requirements")
		}
	}
	return nil
}

func (c *Cache) releasePartRow(ctx context.Context, row *sharedResult) error {
	wcprof.WriteLock(ctx, &c.egraphMu, egraphLockIdent)
	queue, err := c.decrementIncomingOwnershipLocked(ctx, row, nil)
	callbacks, collectErr := c.collectUnownedResultsLocked(ctx, queue)
	c.egraphMu.Unlock()
	return errors.Join(err, collectErr, runOnReleaseFuncs(ctx, callbacks))
}

func isPartTaskKey(key LazyGroupKey) bool {
	return strings.HasPrefix(string(key), "obtain:") || strings.HasPrefix(string(key), "acquire:") || strings.HasPrefix(string(key), "lazy:")
}

// isPartDeliveryTaskKey reports whether a part task only delivers a part:
// acquiring it opens a local copy or obtains one, and obtaining installs it
// from a source. Neither computes the value; an acquire that must compute
// starts a lazy evaluation task, which is a group of its own.
func isPartDeliveryTaskKey(key LazyGroupKey) bool {
	return strings.HasPrefix(string(key), "obtain:") || strings.HasPrefix(string(key), "acquire:")
}

func (t *PartTaskToken) openOwnerSync() {
	t.ownerSyncOnce.Do(func() {
		if t.ownerSync != nil {
			close(t.ownerSync)
		}
	})
}
