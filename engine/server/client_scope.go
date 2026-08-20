package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/dagger/dagger/engine"
)

type clientLifecycleLeaseRecord struct {
	kind    engine.ClientLeaseKind
	ownerID string
}

// RegisterNestedClientTransport delegates one unique nested transport from the
// creating request's held scope. The descriptor and both lifecycle leases are
// published together before the proxy can serve its first request.
func (srv *Server) RegisterNestedClientTransport(
	ctx context.Context,
	metadata *engine.ClientMetadata,
	parentClientID string,
) (_ *engine.NestedClientTransport, rerr error) {
	if metadata == nil {
		return nil, errors.New("nested client metadata is nil")
	}
	if metadata.SessionID == "" || metadata.ClientID == "" {
		return nil, errors.New("nested client metadata is missing session or client ID")
	}
	if parentClientID == "" {
		return nil, errors.New("nested client transport is missing parent client ID")
	}
	scope, ok := engine.ClientScopeFromContext(ctx)
	if !ok {
		return nil, errors.New("nested client transport requires a held parent client scope")
	}
	if scope.SessionID() != metadata.SessionID || scope.ClientID() != parentClientID {
		return nil, fmt.Errorf(
			"parent client scope %q/%q does not match nested registration %q/%q",
			scope.SessionID(), scope.ClientID(), metadata.SessionID, parentClientID,
		)
	}
	if metadata.ClientID == parentClientID {
		return nil, fmt.Errorf("client ancestry cycle: client %q cannot be its own parent", metadata.ClientID)
	}

	srv.daggerSessionsMu.RLock()
	sess := srv.daggerSessions[metadata.SessionID]
	srv.daggerSessionsMu.RUnlock()
	if sess == nil {
		return nil, fmt.Errorf("session %q not found", metadata.SessionID)
	}

	sess.lifecycleMu.Lock()
	defer sess.lifecycleMu.Unlock()
	if sess.state.Load() != sessionStateInitialized {
		return nil, fmt.Errorf("session %q is not initialized", metadata.SessionID)
	}
	if !scope.CanDelegateTo(sess.scopeAuthority) {
		return nil, fmt.Errorf("parent client scope does not belong to the current session %q", metadata.SessionID)
	}

	sess.clientMu.RLock()
	_, exists := sess.clients[metadata.ClientID]
	parent := sess.clients[parentClientID]
	sess.clientMu.RUnlock()
	if exists {
		return nil, fmt.Errorf("nested client %q transport is already registered or permanently closed", metadata.ClientID)
	}
	if parent == nil {
		return nil, fmt.Errorf("parent client %q not found for nested client %q", parentClientID, metadata.ClientID)
	}
	parentIDs, err := sess.parentClientIDsForRegistration(metadata.ClientID, parentClientID, false)
	if err != nil {
		return nil, fmt.Errorf("validate client ancestry: %w", err)
	}

	parentScope, err := scope.Delegate(engine.ClientLeaseChild, metadata.ClientID)
	if err != nil {
		return nil, fmt.Errorf("delegate parent client scope: %w", err)
	}
	defer func() {
		if rerr != nil {
			parentScope.Lease().Release()
		}
	}()

	metadataSnapshot, err := cloneClientMetadata(metadata)
	if err != nil {
		return nil, err
	}
	client := &daggerClient{
		state:                  clientStateUninitialized,
		daggerSession:          sess,
		clientID:               metadata.ClientID,
		clientVersion:          metadata.ClientVersion,
		secretToken:            metadata.ClientSecretToken,
		shutdownCh:             make(chan struct{}),
		clientMetadata:         metadataSnapshot,
		parentClientIDs:        parentIDs,
		accepting:              true,
		lifecycleLeases:        make(map[uint64]clientLifecycleLeaseRecord),
		parentClientScopeLease: parentScope.Lease(),
	}
	transport := engine.NewNestedClientTransport(func() {
		sess.closeNestedClientTransport(client)
	})
	client.nestedTransport = transport

	sess.scopeMu.Lock()
	client.transportLease = sess.newClientLifecycleLeaseLocked(client, engine.ClientLeaseTransport, metadata.ClientID)
	sess.scopeMu.Unlock()

	sess.clientMu.Lock()
	if _, duplicate := sess.clients[metadata.ClientID]; duplicate {
		sess.clientMu.Unlock()
		client.transportLease.Release()
		return nil, fmt.Errorf("nested client %q transport was concurrently registered", metadata.ClientID)
	}
	sess.clients[metadata.ClientID] = client
	sess.clientMu.Unlock()
	return transport, nil
}

func cloneClientMetadata(metadata *engine.ClientMetadata) (*engine.ClientMetadata, error) {
	if metadata == nil {
		return nil, errors.New("client metadata is nil")
	}
	// ClientMetadata is a wire declaration, so a JSON round trip provides a
	// simple exhaustive deep clone of every exported map, slice, pointer, cache,
	// module, and workspace declaration. Restore the one intentionally
	// non-wire, engine-internal field explicitly.
	snapshot, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("snapshot client metadata: %w", err)
	}
	var cloned engine.ClientMetadata
	if err := json.Unmarshal(snapshot, &cloned); err != nil {
		return nil, fmt.Errorf("restore client metadata: %w", err)
	}
	cloned.UseRecipeIDsByDefault = metadata.UseRecipeIDsByDefault
	return &cloned, nil
}

// mergeClientMetadataLocked monotonically completes a client's bootstrap
// metadata. sess.scopeMu is the serialization point: it also protects sealing,
// root scope acquisition, transport close, and session teardown.
func (sess *daggerSession) mergeClientMetadataLocked(client *daggerClient, incoming *engine.ClientMetadata) error {
	if incoming == nil {
		return errors.New("client metadata is nil")
	}
	candidate, err := cloneClientMetadata(incoming)
	if err != nil {
		return err
	}
	if client.clientMetadata == nil {
		if client.metadataSealed {
			return fmt.Errorf("client %q has sealed empty metadata", client.clientID)
		}
		client.clientMetadata = candidate
		return nil
	}

	stored := reflect.ValueOf(client.clientMetadata).Elem()
	update := reflect.ValueOf(candidate).Elem()
	typeOfMetadata := stored.Type()
	for i := 0; i < stored.NumField(); i++ {
		incomingField := update.Field(i)
		if incomingField.IsZero() {
			continue
		}
		storedField := stored.Field(i)
		fieldName := typeOfMetadata.Field(i).Name
		if storedField.IsZero() {
			if client.metadataSealed {
				return fmt.Errorf("client %q metadata is sealed; field %s cannot be completed", client.clientID, fieldName)
			}
			storedField.Set(incomingField)
			continue
		}
		if !reflect.DeepEqual(storedField.Interface(), incomingField.Interface()) {
			return fmt.Errorf("client %q metadata field %s conflicts with bootstrap value", client.clientID, fieldName)
		}
	}
	return nil
}

// sealClientMetadataLocked freezes one authoritative snapshot. Re-cloning at
// the boundary ensures no alias retained by an earlier bootstrap caller can
// mutate the value later. sess.scopeMu must be held.
func (sess *daggerSession) sealClientMetadataLocked(client *daggerClient) error {
	if client.metadataSealed {
		return nil
	}
	if client.clientMetadata == nil {
		return fmt.Errorf("client %q has no metadata to seal", client.clientID)
	}
	if !client.accepting {
		return fmt.Errorf("client %q is closed", client.clientID)
	}
	if client.nestedTransport != nil && client.nestedTransport.Closed() {
		return fmt.Errorf("nested client %q transport is closed", client.clientID)
	}
	snapshot, err := cloneClientMetadata(client.clientMetadata)
	if err != nil {
		return err
	}
	client.clientMetadata = snapshot
	client.clientVersion = snapshot.ClientVersion
	client.secretToken = snapshot.ClientSecretToken
	client.metadataSealed = true
	return nil
}

// clientMetadataSnapshot returns an independent copy of sealed metadata for
// lookups outside the runtime. Callers can mutate it without changing future
// scopes or lookups.
func (sess *daggerSession) clientMetadataSnapshot(client *daggerClient) (*engine.ClientMetadata, error) {
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	if !client.metadataSealed {
		return nil, fmt.Errorf("client %q metadata is not sealed", client.clientID)
	}
	return cloneClientMetadata(client.clientMetadata)
}

func (sess *daggerSession) closeNestedClientTransport(client *daggerClient) {
	sess.scopeMu.Lock()
	client.accepting = false
	transport := client.transportLease
	client.transportLease = nil
	sess.scopeMu.Unlock()
	transport.Release()
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
	if !client.metadataSealed {
		return engine.ClientScope{}, fmt.Errorf("client %q metadata is not sealed", client.clientID)
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
	if sess.scopeAuthority == nil {
		sess.scopeAuthority = engine.NewClientScopeAuthority()
	}
	sess.nextScopeID++
	leaseID := sess.nextScopeID
	if client.lifecycleLeases == nil {
		client.lifecycleLeases = make(map[uint64]clientLifecycleLeaseRecord)
	}
	client.lifecycleLeases[leaseID] = clientLifecycleLeaseRecord{kind: kind, ownerID: ownerID}
	return engine.NewClientLifecycleLeaseWithDelegation(
		kind,
		ownerID,
		func() { sess.releaseClientLifecycleLease(client, leaseID) },
		func(cloneKind engine.ClientLeaseKind, cloneOwnerID string) (*engine.ClientLifecycleLease, error) {
			return sess.cloneClientLifecycleLease(client, leaseID, cloneKind, cloneOwnerID)
		},
		func(sessionID, clientID string, cloneKind engine.ClientLeaseKind, cloneOwnerID string) (*engine.ClientLifecycleLease, error) {
			return sess.delegateClientLifecycleLease(client, leaseID, sessionID, clientID, cloneKind, cloneOwnerID)
		},
		sess.scopeAuthority,
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

func (sess *daggerSession) delegateClientLifecycleLease(
	client *daggerClient,
	sourceID uint64,
	sessionID, clientID string,
	kind engine.ClientLeaseKind,
	ownerID string,
) (*engine.ClientLifecycleLease, error) {
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	if sess.state.Load() == sessionStateRemoved {
		return nil, fmt.Errorf("session %q is closed", sess.sessionID)
	}
	if sessionID != sess.sessionID || clientID != client.clientID {
		return nil, fmt.Errorf("client scope identity %q/%q does not match %q/%q", sessionID, clientID, sess.sessionID, client.clientID)
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
	var parentScopes []*engine.ClientLifecycleLease
	sess.scopeMu.Lock()
	for _, client := range clients {
		client.accepting = false
		if client.transportLease != nil {
			transports = append(transports, client.transportLease)
			client.transportLease = nil
		}
		if client.parentClientScopeLease != nil {
			parentScopes = append(parentScopes, client.parentClientScopeLease)
			client.parentClientScopeLease = nil
		}
	}
	sess.scopeMu.Unlock()
	for _, transport := range transports {
		transport.Release()
	}
	// Child quiescence is not implemented yet. Keep each parent's child lease
	// visible for the whole child record lifetime and release it only at the
	// authoritative session teardown boundary.
	for _, parentScope := range parentScopes {
		parentScope.Release()
	}
}

func (sess *daggerSession) clientMetadataSealed(client *daggerClient) bool {
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	return client.metadataSealed
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
