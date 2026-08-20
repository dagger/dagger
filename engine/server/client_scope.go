package server

import (
	"fmt"

	"github.com/dagger/dagger/engine"
)

type clientLifecycleLeaseRecord struct {
	kind    engine.ClientLeaseKind
	ownerID string
}

// acquireRootClientScope acquires work directly from a reachable client. Root
// acquisition is rejected after client close or session teardown; background
// work must clone an already-held scope instead.
func (sess *daggerSession) acquireRootClientScope(
	client *daggerClient,
	kind engine.ClientLeaseKind,
	ownerID string,
) (engine.ClientScope, error) {
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()

	if sess.state.Load() == sessionStateRemoved {
		return engine.ClientScope{}, fmt.Errorf("session %q is closed", sess.sessionID)
	}
	if !client.accepting {
		return engine.ClientScope{}, fmt.Errorf("client %q is closed", client.clientID)
	}
	lease := sess.newClientLifecycleLeaseLocked(client, kind, ownerID)
	scope, err := engine.NewClientScope(client.clientMetadata, lease)
	if err != nil {
		delete(client.lifecycleLeases, sess.nextScopeID)
		return engine.ClientScope{}, err
	}
	return scope, nil
}

// newClientLifecycleLeaseLocked publishes one lease. sess.scopeMu must be held.
func (sess *daggerSession) newClientLifecycleLeaseLocked(
	client *daggerClient,
	kind engine.ClientLeaseKind,
	ownerID string,
) *engine.ClientLifecycleLease {
	sess.nextScopeID++
	leaseID := sess.nextScopeID
	if client.lifecycleLeases == nil {
		client.lifecycleLeases = make(map[uint64]clientLifecycleLeaseRecord)
	}
	client.lifecycleLeases[leaseID] = clientLifecycleLeaseRecord{kind: kind, ownerID: ownerID}
	return engine.NewClientLifecycleLease(
		kind,
		ownerID,
		func() { sess.releaseClientLifecycleLease(client, leaseID) },
		func(cloneKind engine.ClientLeaseKind, cloneOwnerID string) (*engine.ClientLifecycleLease, error) {
			return sess.cloneClientLifecycleLease(client, leaseID, cloneKind, cloneOwnerID)
		},
	)
}

func (sess *daggerSession) cloneClientLifecycleLease(
	client *daggerClient,
	sourceID uint64,
	kind engine.ClientLeaseKind,
	ownerID string,
) (*engine.ClientLifecycleLease, error) {
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	if sess.state.Load() == sessionStateRemoved {
		return nil, fmt.Errorf("session %q is closed", sess.sessionID)
	}
	if _, ok := client.lifecycleLeases[sourceID]; !ok {
		return nil, fmt.Errorf("client %q scope is no longer held", client.clientID)
	}
	return sess.newClientLifecycleLeaseLocked(client, kind, ownerID), nil
}

func (sess *daggerSession) releaseClientLifecycleLease(client *daggerClient, leaseID uint64) {
	sess.scopeMu.Lock()
	delete(client.lifecycleLeases, leaseID)
	sess.scopeMu.Unlock()
}

// closeClientScope closes root acquisition and releases reachability. Existing
// scopes remain cloneable until their own leases are released.
func (sess *daggerSession) closeClientScope(client *daggerClient) {
	sess.scopeMu.Lock()
	client.accepting = false
	transport := client.transportLease
	client.transportLease = nil
	sess.scopeMu.Unlock()
	transport.Release()
}

// beginClientScopeTeardown prevents every client from accepting new root work.
// Existing leased work drains through its normal terminal transitions.
func (sess *daggerSession) beginClientScopeTeardown() {
	sess.clientMu.RLock()
	clients := make([]*daggerClient, 0, len(sess.clients))
	for _, client := range sess.clients {
		clients = append(clients, client)
	}
	sess.clientMu.RUnlock()

	var transports []*engine.ClientLifecycleLease
	sess.scopeMu.Lock()
	for _, client := range clients {
		client.accepting = false
		if client.transportLease != nil {
			transports = append(transports, client.transportLease)
			client.transportLease = nil
		}
	}
	sess.scopeMu.Unlock()
	for _, transport := range transports {
		transport.Release()
	}
}

func (sess *daggerSession) clientLifecycleSnapshot(client *daggerClient) (bool, bool, []clientLifecycleLeaseRecord) {
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	out := make([]clientLifecycleLeaseRecord, 0, len(client.lifecycleLeases))
	for _, lease := range client.lifecycleLeases {
		out = append(out, lease)
	}
	return client.accepting, client.lifecycleLeases != nil, out
}
