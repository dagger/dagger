package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/fixturetransport"
	bkcache "github.com/dagger/dagger/engine/snapshots"
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
// their original deadlines and their Done channels all stay authoritative.
// Each delivered record has one watcher on the request's own Done, which
// drops the record when it closes whether or not the harness ever took it, so
// the records never outnumber the bridge's live exchanges. Run joins the
// watchers and clears everything when it ends.
type remoteCacheFixtureController struct {
	srv *Server

	mu           sync.Mutex
	gcGeneration uint64
	adapter      *RemoteCacheAdapter
	armed        map[digest.Digest]core.RemoteCacheFixtureRenewalReply
	delivered    []*dagql.RenewalRequest
	// ready closes whenever delivered or stopped changes.
	ready    chan struct{}
	stopped  bool
	renewals core.RemoteCacheFixtureRenewals
	// observationCap bounds the staged-reply history, like every other
	// observation of a scenario. Zero is the default bound.
	observationCap int
}

// remoteCacheFixtureObservationCap is the default bound of one scenario.
const remoteCacheFixtureObservationCap = 1 << 16

var errRemoteCacheFixtureStopped = errors.New("remote cache fixture: the integration has stopped")

// wakeLocked wakes every waiting take to look again.
func (f *remoteCacheFixtureController) wakeLocked() {
	close(f.ready)
	f.ready = make(chan struct{})
}

// retire drops a delivered record nobody took. It is a no-op for one that a
// take already removed.
func (f *remoteCacheFixtureController) retire(request *dagql.RenewalRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, delivered := range f.delivered {
		if delivered == request {
			f.delivered = slices.Delete(f.delivered, i, i+1)
			f.renewals.Retired++
			f.wakeLocked()
			return
		}
	}
}

var errRemoteCacheFixtureDisabled = core.ErrRemoteCacheFixtureNoController

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
	// Run must return once its lifetime ends. An armed reply paused at a
	// fixture barrier is inside this goroutine, and only a detachment ends
	// that pause, which the server's wrapper does only after Run returns. So
	// the lifetime's end detaches this adapter itself; close is idempotent
	// and is what the wrapper would do next anyway.
	stopDetach := context.AfterFunc(ctx, func() { adapter.close(context.Cause(ctx)) })
	defer stopDetach()
	var watchers sync.WaitGroup
	defer func() {
		// Every watcher ends with its request's Done or with ctx, and Run only
		// returns once one of those holds for all of them.
		watchers.Wait()
		f.mu.Lock()
		f.delivered = nil
		f.armed = map[digest.Digest]core.RemoteCacheFixtureRenewalReply{}
		f.stopped = true
		f.wakeLocked()
		f.mu.Unlock()
	}()
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
			f.renewals.Delivered++
			f.wakeLocked()
		}
		f.mu.Unlock()
		if !armed {
			watchers.Add(1)
			go func() {
				defer watchers.Done()
				select {
				case <-request.Done:
				case <-ctx.Done():
				}
				f.retire(request)
			}()
			continue
		}
		// The harness staged this reply before the triggering demand. Only
		// the exchange ID is filled in here; the mailbox, the original
		// deadline and the reply's validation are all still the real ones.
		// The controller is the only witness of what became of it.
		disposition := adapter.ReplyRenewal(dagql.RenewalReply{ID: request.ID, Chain: template.Chain, Unavailable: template.Unavailable, Addresses: template.Addresses})
		f.mu.Lock()
		limit := f.observationCap
		if limit == 0 {
			limit = remoteCacheFixtureObservationCap
		}
		if len(f.renewals.ArmedReplies) >= limit {
			// Never dropped silently: the report fails on this.
			f.renewals.Overflowed = true
		} else {
			f.renewals.ArmedReplies = append(f.renewals.ArmedReplies, core.RemoteCacheFixtureArmedReply{Chain: request.Chain, Sequence: request.ID.Sequence, Disposition: renewalDispositionName(disposition)})
		}
		f.mu.Unlock()
	}
}

func renewalDispositionName(disposition dagql.RenewalReplyDisposition) string {
	if disposition == dagql.RenewalReplyAccepted {
		return "accepted"
	}
	return "discarded"
}

// RemoteCacheFixtureObserve starts a new observation scope: the counters and
// the staged-reply history are cleared and the bound is set. Live delivered
// records and staged replies not yet used are state, not observations, and
// stay.
func (srv *Server) RemoteCacheFixtureObserve(limit int) error {
	f, err := srv.fixtureController()
	if err != nil {
		return err
	}
	if limit <= 0 {
		return fmt.Errorf("observe requires a positive cap")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observationCap = limit
	f.renewals = core.RemoteCacheFixtureRenewals{}
	return nil
}

func (srv *Server) RemoteCacheFixtureRenewals() (core.RemoteCacheFixtureRenewals, error) {
	f, err := srv.fixtureController()
	if err != nil {
		return core.RemoteCacheFixtureRenewals{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	report := f.renewals
	report.Live = len(f.delivered)
	report.ArmedPending = len(f.armed)
	report.ArmedReplies = append([]core.RemoteCacheFixtureArmedReply{}, f.renewals.ArmedReplies...)
	return report, nil
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
				// Its Done closed before its watcher ran; never handed out.
				f.renewals.Retired++
			default:
				request = next
				f.renewals.Taken++
			}
		}
		ready, stopped := f.ready, f.stopped
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
		if stopped {
			return core.RemoteCacheFixtureRenewal{}, errRemoteCacheFixtureStopped
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
	disposition := adapter.ReplyRenewal(dagql.RenewalReply{ID: id, Chain: reply.Chain, Unavailable: reply.Unavailable, Addresses: reply.Addresses})
	f.mu.Lock()
	if disposition == dagql.RenewalReplyAccepted {
		f.renewals.RepliesAccepted++
	} else {
		f.renewals.RepliesDiscarded++
	}
	f.mu.Unlock()
	return renewalDispositionName(disposition), nil
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

func (srv *Server) RemoteCacheFixtureStorage(ctx context.Context) (core.RemoteCacheFixtureStorage, error) {
	if _, err := srv.fixtureController(); err != nil {
		return core.RemoteCacheFixtureStorage{}, err
	}
	var report core.RemoteCacheFixtureStorage
	var err error
	if report.Snapshots, report.Blobs, _, err = srv.fixtureStorageCounts(ctx); err != nil {
		return report, err
	}
	all, err := srv.leaseManager.List(ctx)
	if err != nil {
		return report, fmt.Errorf("list leases: %w", err)
	}
	report.OwnerLeases = []string{}
	for _, lease := range all {
		switch {
		case bkcache.IsTransferLease(lease):
			report.TransientPins++
			held := lease.ID + ":"
			resources, err := srv.leaseManager.ListResources(ctx, lease)
			if err != nil {
				held += " " + err.Error()
			}
			for _, resource := range resources {
				held += " " + resource.Type + "/" + resource.ID
			}
			report.TransientPinResources = append(report.TransientPinResources, held)
		case strings.HasPrefix(lease.ID, "dagql/result/"):
			report.OwnerLeases = append(report.OwnerLeases, lease.ID)
		default:
			report.OtherLeases++
		}
	}
	slices.Sort(report.OwnerLeases)
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
