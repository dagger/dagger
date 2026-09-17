package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/fixturetransport"
	"github.com/opencontainers/go-digest"
)

// remoteCacheFixtureController is the enabled engine's one test fixture
// controller, for its cache's lifetime. It exists only when the fixture gate
// is set: a disabled engine has no controller, no renewal consumer and no
// journal work, and every control below then reports that.
//
// It supplies the real integration point's Run. That one loop calls the real
// TakeRenewalRequest and either answers a request itself from a pre-staged
// reply armed for its chain fingerprint, or hands it to a waiting fixture
// call. It is not a second mailbox: the bridge's own bound of live exchanges,
// their original deadlines and their Done channels all stay authoritative,
// and a delivered record is dropped when its Done closes whether or not the
// harness ever took it.
type remoteCacheFixtureController struct {
	srv *Server

	mu           sync.Mutex
	gcGeneration uint64
	adapter      *RemoteCacheAdapter
	armed        map[digest.Digest]core.RemoteCacheFixtureRenewalReply
	delivered    []*dagql.RenewalRequest
	ready        chan struct{}
}

var errRemoteCacheFixtureDisabled = errors.New("remote cache fixture is not enabled")

func remoteCacheFixtureEnabled() bool {
	return os.Getenv(core.RemoteCacheFixtureRootEnv) != ""
}

// enableRemoteCacheFixtureTransports runs once at startup, under the fixture
// gate and before any request: it enables the in-process dispatcher, which
// the two HTTP clients and the content source then wrap their transports
// with, re-registers go-git's HTTP clients around it, and maps the fixture's
// Git host to copied bare repositories for the real Git executable. A
// disabled engine does none of this.
func enableRemoteCacheFixtureTransports() error {
	root := os.Getenv(core.RemoteCacheFixtureRootEnv)
	if root == "" {
		return nil
	}
	if _, err := fixturetransport.Enable(root); err != nil {
		return err
	}
	schema.InstallRemoteCacheFixtureGitTransport()
	core.EnableRemoteCacheFixtureGit(root)
	return nil
}

// remoteCacheFixtureIntegration returns the integration the engine starts:
// the configured one, or, when none is configured and the gate is set, the
// fixture controller's consumer loop.
func (srv *Server) remoteCacheFixtureIntegration(cfg *RemoteCacheIntegrationConfig) *RemoteCacheIntegrationConfig {
	if cfg != nil || !remoteCacheFixtureEnabled() {
		return cfg
	}
	controller := &remoteCacheFixtureController{srv: srv, armed: map[digest.Digest]core.RemoteCacheFixtureRenewalReply{}, ready: make(chan struct{})}
	srv.remoteCacheFixture = controller
	return &RemoteCacheIntegrationConfig{Run: controller.run}
}

func (f *remoteCacheFixtureController) run(ctx context.Context, adapter *RemoteCacheAdapter) error {
	f.mu.Lock()
	f.adapter = adapter
	f.mu.Unlock()
	for {
		request, err := adapter.TakeRenewalRequest(ctx)
		if err != nil {
			return err
		}
		f.mu.Lock()
		template, armed := f.armed[request.Chain]
		if armed {
			delete(f.armed, request.Chain)
		} else {
			f.delivered = append(f.delivered, request)
			close(f.ready)
			f.ready = make(chan struct{})
		}
		f.mu.Unlock()
		if armed {
			// The harness staged this reply before the triggering demand. Only
			// the exchange ID is filled in here; the mailbox, the original
			// deadline and the reply's validation are all still the real ones.
			adapter.ReplyRenewal(dagql.RenewalReply{ID: request.ID, Chain: template.Chain, Unavailable: template.Unavailable, Addresses: template.Addresses})
		}
	}
}

func (srv *Server) fixtureController() (*remoteCacheFixtureController, error) {
	if srv.remoteCacheFixture == nil {
		return nil, errRemoteCacheFixtureDisabled
	}
	return srv.remoteCacheFixture, nil
}

func (srv *Server) RemoteCacheFixtureOffer(ctx context.Context, receiver dagql.AnyResult, offers []dagql.PersistedPartOffer) ([]dagql.OfferDisposition, error) {
	f, err := srv.fixtureController()
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	adapter := f.adapter
	f.mu.Unlock()
	if adapter == nil {
		return nil, fmt.Errorf("remote cache fixture: the integration has not started")
	}
	return adapter.OfferParts(ctx, receiver, offers)
}

func (srv *Server) RemoteCacheFixtureArmRenewalReply(template core.RemoteCacheFixtureRenewalReply) error {
	f, err := srv.fixtureController()
	if err != nil {
		return err
	}
	if template.Epoch != "" || template.Sequence != 0 {
		return fmt.Errorf("an armed renewal reply names no exchange; the consumer loop fills it in")
	}
	if template.Chain == "" && len(template.Layers) > 0 {
		if template.Chain, err = dagql.RenewalChainFingerprint(template.Layers); err != nil {
			return fmt.Errorf("armed renewal reply layers: %w", err)
		}
	}
	template.Layers = nil
	if err := template.Chain.Validate(); err != nil {
		return fmt.Errorf("armed renewal reply chain: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armed[template.Chain] = template
	return nil
}

func (srv *Server) RemoteCacheFixtureTakeRenewal(ctx context.Context) (core.RemoteCacheFixtureRenewal, error) {
	f, err := srv.fixtureController()
	if err != nil {
		return core.RemoteCacheFixtureRenewal{}, err
	}
	for {
		f.mu.Lock()
		var request *dagql.RenewalRequest
		for len(f.delivered) > 0 && request == nil {
			next := f.delivered[0]
			f.delivered = f.delivered[1:]
			select {
			case <-next.Done:
				// Retired by its own Done; never handed out afterwards.
			default:
				request = next
			}
		}
		ready := f.ready
		f.mu.Unlock()
		if request != nil {
			return core.RemoteCacheFixtureRenewal{
				Epoch:             hex.EncodeToString(request.ID.Epoch[:]),
				Sequence:          request.ID.Sequence,
				Chain:             request.Chain,
				RenewalKey:        request.RenewalKey,
				Layers:            request.Layers,
				NeededBlob:        request.NeededBlob,
				RemainingDeadline: time.Until(request.Deadline),
			}, nil
		}
		select {
		case <-ready:
		case <-ctx.Done():
			return core.RemoteCacheFixtureRenewal{}, context.Cause(ctx)
		}
	}
}

func (srv *Server) RemoteCacheFixtureReplyRenewal(reply core.RemoteCacheFixtureRenewalReply) (string, error) {
	f, err := srv.fixtureController()
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	adapter := f.adapter
	f.mu.Unlock()
	if adapter == nil {
		return "", fmt.Errorf("remote cache fixture: the integration has not started")
	}
	var id dagql.RenewalRequestID
	epoch, err := hex.DecodeString(reply.Epoch)
	if err != nil || len(epoch) != len(id.Epoch) {
		return "", fmt.Errorf("renewal reply epoch is not %d hex bytes", len(id.Epoch))
	}
	copy(id.Epoch[:], epoch)
	id.Sequence = reply.Sequence
	if adapter.ReplyRenewal(dagql.RenewalReply{ID: id, Chain: reply.Chain, Unavailable: reply.Unavailable, Addresses: reply.Addresses}) == dagql.RenewalReplyAccepted {
		return "accepted", nil
	}
	return "discarded", nil
}

func (srv *Server) RemoteCacheFixtureGC(ctx context.Context) (core.RemoteCacheFixtureGC, error) {
	f, err := srv.fixtureController()
	if err != nil {
		return core.RemoteCacheFixtureGC{}, err
	}
	// The engine's existing GC serialization.
	srv.gcmu.Lock()
	defer srv.gcmu.Unlock()
	var report core.RemoteCacheFixtureGC
	if report.SnapshotsBefore, report.BlobsBefore, report.LeasesBefore, err = srv.fixtureStorageCounts(ctx); err != nil {
		return report, err
	}
	if _, err := srv.containerdMetaDB.GarbageCollect(ctx); err != nil {
		return report, fmt.Errorf("metadata garbage collection: %w", err)
	}
	if report.SnapshotsAfter, report.BlobsAfter, report.LeasesAfter, err = srv.fixtureStorageCounts(ctx); err != nil {
		return report, err
	}
	f.mu.Lock()
	f.gcGeneration++
	report.Generation = f.gcGeneration
	f.mu.Unlock()
	return report, nil
}

func (srv *Server) fixtureStorageCounts(ctx context.Context) (snapshotCount, blobs, leaseCount uint64, err error) {
	// The metadata database's own views need the engine's namespace, the one
	// its snapshotter, content store and lease manager are all opened with.
	ctx = namespaces.WithNamespace(ctx, "dagger")
	if err := srv.containerdMetaDB.Snapshotter(srv.snapshotterName).Walk(ctx, func(context.Context, snapshots.Info) error {
		snapshotCount++
		return nil
	}); err != nil {
		return 0, 0, 0, fmt.Errorf("count snapshots: %w", err)
	}
	if err := srv.containerdMetaDB.ContentStore().Walk(ctx, func(content.Info) error {
		blobs++
		return nil
	}); err != nil {
		return 0, 0, 0, fmt.Errorf("count content: %w", err)
	}
	all, err := srv.leaseManager.List(ctx)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("count leases: %w", err)
	}
	return snapshotCount, blobs, uint64(len(all)), nil
}
