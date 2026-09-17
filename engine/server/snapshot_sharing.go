package server

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

// snapshotSharingReceivesImports reports whether this engine can ever hold an
// imported row, which is the only kind of row early sharing can fill. A
// configured remote-cache integration can receive them; so can the
// environment-gated transfer fixture. Every other engine leaves sharing off,
// registers no preparation callback and builds no core schema base early, so
// its startup is unchanged and its notification hooks cost one flag check.
func snapshotSharingReceivesImports(opts *NewServerOpts) bool {
	if opts != nil && opts.RemoteCacheIntegration != nil {
		return true
	}
	_, gated := os.LookupEnv(core.RemoteCacheFixtureRootEnv)
	return gated
}

// initSnapshotSharing builds the static core schema base, registers the
// sharing preparation context and then enables sharing admission, in that
// order. It runs after local cache initialization has restored durable
// owners and released the previous process's transfer pins, and before the
// remote-cache integration attaches, so external control can never precede
// registration.
func (srv *Server) initSnapshotSharing(ctx context.Context, opts *NewServerOpts) error {
	if srv.engineCache == nil || !snapshotSharingReceivesImports(opts) {
		return nil
	}
	prepare, err := srv.snapshotSharePreparation(ctx)
	if err != nil {
		return err
	}
	if err := srv.engineCache.SetPartPreparationContext(prepare); err != nil {
		return fmt.Errorf("register snapshot share preparation: %w", err)
	}
	if err := srv.engineCache.EnableSnapshotSharing(); err != nil {
		return fmt.Errorf("enable snapshot sharing: %w", err)
	}
	return nil
}

// snapshotSharePreparation builds the static core schema base and returns the
// preparation callback over it.
func (srv *Server) snapshotSharePreparation(ctx context.Context) (dagql.PartPreparationContext, error) {
	// Building the base here rather than on the first client is the one
	// startup change this engine pays: core schema installation moves to
	// startup, and a base-construction failure fails NewServer.
	base, err := srv.getCoreSchemaBase(ctx)
	if err != nil {
		return nil, fmt.Errorf("initialize core schema base for snapshot sharing: %w", err)
	}
	root := core.NewRoot(srv)
	view := call.View(engine.BaseVersion(engine.NormalizeVersion(engine.Version)))
	var (
		forkOnce sync.Once
		forked   *dagql.Server
		forkErr  error
	)
	prepare := func(ctx context.Context) (context.Context, *dagql.Server, error) {
		// The schema-only fork is built on first use and retained for the
		// engine lifetime; it installs no user module and builds no dynamic
		// schema. The callback captures only the cache, the engine root, the
		// immutable native core registrations and the pure default factory:
		// no client and no session.
		forkOnce.Do(func() {
			forked, forkErr = base.ForkForPersistedDecode(ctx, root, view)
		})
		if forkErr != nil {
			return nil, nil, forkErr
		}
		ctx = core.ContextWithQuery(ctx, root)
		ctx = core.ContextWithPersistedDecodeDefaults(ctx, root, func(v call.View) *core.SchemaBuilder {
			return core.NewSchemaBuilder(root, []core.Mod{base.CoreMod(v)})
		})
		// Root and factory agreement is settled here, before the cache can
		// start a shared decode attempt with this context, not discovered
		// inside one by the Module decoder.
		if err := core.CheckPersistedDecodeDefaults(ctx, forked); err != nil {
			return nil, nil, err
		}
		return ctx, forked, nil
	}
	return prepare, nil
}
