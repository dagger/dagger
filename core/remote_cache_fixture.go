package core

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/fixturetransport"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/opencontainers/go-digest"
)

const RemoteCacheFixtureRootEnv = "_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT"

// RemoteCacheFixturePersistence is diagnostic state stored in the gated test
// fixture directory, independently of the cache that a reset can discard.
type RemoteCacheFixturePersistence struct {
	PersistenceResetReason    dagql.CachePersistenceResetReason `json:"persistenceResetReason"`
	LocalCacheResetReason     string                            `json:"localCacheResetReason"`
	RemovedPersistedRootCount int                               `json:"removedPersistedRootCount"`
	DiskPrunedResults         []string                          `json:"diskPrunedResults,omitempty"`
}

// RemoteCacheFixtureControls are the gated test fixture's server-only
// controls. The schema handler reaches them through an optional interface on
// Query.Server, so core does not import engine/server. An engine started
// without the fixture gate does not implement any of them usefully: every
// method then reports that the fixture is not enabled.
type RemoteCacheFixtureControls interface {
	// RemoteCacheFixtureGC runs the engine's actual metadata and snapshot
	// garbage collection under its existing serialization.
	RemoteCacheFixtureGC(context.Context) (RemoteCacheFixtureGC, error)
	// RemoteCacheFixtureStorage reads the report's storage group.
	RemoteCacheFixtureStorage(context.Context) (RemoteCacheFixtureStorage, error)
	// RemoteCacheFixtureOffer makes the real adapter OfferParts call.
	RemoteCacheFixtureOffer(context.Context, dagql.AnyResult, []dagql.PersistedPartOffer) ([]dagql.OfferDisposition, error)
	// RemoteCacheFixtureTakeRenewal awaits the next renewal request the real
	// consumer loop delivered and no armed reply answered.
	RemoteCacheFixtureTakeRenewal(context.Context) (RemoteCacheFixtureRenewal, error)
	// RemoteCacheFixtureReplyRenewal calls the real ReplyRenewal exactly once.
	RemoteCacheFixtureReplyRenewal(RemoteCacheFixtureRenewalReply) (string, error)
	// RemoteCacheFixtureArmRenewalReply stores a pre-staged reply, keyed by
	// the chain fingerprint it expects, for the consumer loop to send itself.
	RemoteCacheFixtureArmRenewalReply(RemoteCacheFixtureRenewalReply) error
	// RemoteCacheFixtureRenewals reads the report's renewal group.
	RemoteCacheFixtureRenewals() (RemoteCacheFixtureRenewals, error)
}

// RemoteCacheFixtureRenewals is the report's renewal group: what the
// fixture's one consumer loop did with the real mailbox's requests. The
// exchanges themselves are in the report's reached points.
type RemoteCacheFixtureRenewals struct {
	// Delivered counts requests the loop took that no armed reply answered.
	Delivered uint64 `json:"delivered"`
	// Taken counts delivered requests handed to a takeRenewal call; Retired
	// counts those whose own Done ended them first. Live is the rest.
	Taken   uint64 `json:"taken"`
	Retired uint64 `json:"retired"`
	Live    int    `json:"live"`
	// ArmedPending is how many staged replies no request has used yet.
	ArmedPending int `json:"armedPending"`
	// ArmedReplies is every staged reply the loop sent, with what the real
	// ReplyRenewal did with it. A discarded reply fails a success case even
	// if the Lazy operation then produced the bytes.
	ArmedReplies []RemoteCacheFixtureArmedReply `json:"armedReplies"`
	// RepliesAccepted and RepliesDiscarded count replyRenewal calls.
	RepliesAccepted  uint64 `json:"repliesAccepted"`
	RepliesDiscarded uint64 `json:"repliesDiscarded"`
}

// RemoteCacheFixtureArmedReply is one staged reply the consumer loop sent.
type RemoteCacheFixtureArmedReply struct {
	Chain       digest.Digest `json:"chain"`
	Sequence    uint64        `json:"sequence"`
	Disposition string        `json:"disposition"`
}

// RemoteCacheFixtureGC reports one completed collection. The counts are read
// before and after it; they are observations, not a deletion log.
type RemoteCacheFixtureGC struct {
	Generation      uint64 `json:"generation"`
	SnapshotsBefore uint64 `json:"snapshotsBefore"`
	SnapshotsAfter  uint64 `json:"snapshotsAfter"`
	BlobsBefore     uint64 `json:"blobsBefore"`
	BlobsAfter      uint64 `json:"blobsAfter"`
	LeasesBefore    uint64 `json:"leasesBefore"`
	LeasesAfter     uint64 `json:"leasesAfter"`
}

// RemoteCacheFixtureStorage is the report's storage group: what the engine's
// real stores hold now. OwnerLeases are the result owner leases by ID, so a
// test can name a receiver's role lease; TransientPins counts the temporary
// transfer leases that pin a snapshot or a chain only while work is running.
type RemoteCacheFixtureStorage struct {
	Snapshots     uint64   `json:"snapshots"`
	Blobs         uint64   `json:"blobs"`
	OwnerLeases   []string `json:"ownerLeases"`
	TransientPins uint64   `json:"transientPins"`
	// TransientPinResources names what each transient pin holds, as
	// "lease: type/id ...", so a leftover pin can be traced to its snapshot.
	TransientPinResources []string `json:"transientPinResources,omitempty"`
	OtherLeases           uint64   `json:"otherLeases"`
}

// RemoteCacheFixtureRenewal is the serializable part of a delivered renewal
// request. Its runtime Done stays in the engine's controller.
type RemoteCacheFixtureRenewal struct {
	Epoch             string                  `json:"epoch"`
	Sequence          uint64                  `json:"sequence"`
	Chain             digest.Digest           `json:"chain"`
	RenewalKey        string                  `json:"renewalKey"`
	Layers            []snapshots.ExportLayer `json:"layers"`
	NeededBlob        digest.Digest           `json:"neededBlob"`
	RemainingDeadline time.Duration           `json:"remainingDeadlineNanos"`
}

// RemoteCacheFixtureRenewalReply is a reply record, or an armed template: a
// template leaves Epoch and Sequence empty and the consumer loop fills them
// from the request whose chain fingerprint matches.
type RemoteCacheFixtureRenewalReply struct {
	Epoch    string `json:"epoch,omitempty"`
	Sequence uint64 `json:"sequence,omitempty"`
	// Chain is the exact chain fingerprint. A template may give Layers
	// instead, and the controller computes the real fingerprint from them.
	Chain       digest.Digest                       `json:"chain,omitempty"`
	Layers      []snapshots.ExportLayer             `json:"layers,omitempty"`
	Unavailable bool                                `json:"unavailable,omitempty"`
	Addresses   map[digest.Digest]dagql.BlobAddress `json:"addresses,omitempty"`
}

// remoteCacheFixtureGitRoot is set once, at startup, under the fixture gate.
var remoteCacheFixtureGitRoot atomic.Pointer[string]

// EnableRemoteCacheFixtureGit maps the fixture's one Git host to copied bare
// repositories under <root>/git for the real Git executable. The engine calls
// it at startup under the fixture gate. It supplies configuration to GitCLI's
// isolated environment through WithConfig and replaces nothing: not GitCLI's
// Run, not the Git executable, not checkout or snapshot construction. The
// public URL, SHA and options of a repository are unchanged. The mapping is
// bounded to that host and that directory.
func EnableRemoteCacheFixtureGit(root string) {
	remoteCacheFixtureGitRoot.Store(&root)
}

// remoteCacheFixtureGitOptions is nil off-gate.
func remoteCacheFixtureGitOptions() []gitutil.Option {
	root := remoteCacheFixtureGitRoot.Load()
	if root == nil {
		return nil
	}
	return []gitutil.Option{gitutil.WithConfig(map[string]string{
		"url.file://" + filepath.Join(*root, "git") + "/.insteadOf": "https://" + fixturetransport.GitHost + "/",
		"protocol.file.allow": "always",
	})}
}
