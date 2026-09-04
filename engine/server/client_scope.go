package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
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
) (*engine.NestedClientTransport, error) {
	return srv.registerNestedClientTransport(ctx, metadata, parentClientID, "")
}

// RegisterNestedClientTransportForExec additionally binds a logical nested
// client to the exact bootstrap attachables channel created by the same exec.
// The binding is internal proxy state and cannot come from request metadata.
func (srv *Server) RegisterNestedClientTransportForExec(
	ctx context.Context,
	metadata *engine.ClientMetadata,
	parentClientID string,
	attachablesClientID string,
) (*engine.NestedClientTransport, error) {
	if attachablesClientID == "" {
		return nil, errors.New("nested exec transport is missing its attachables client ID")
	}
	return srv.registerNestedClientTransport(ctx, metadata, parentClientID, attachablesClientID)
}

func (srv *Server) registerNestedClientTransport(
	ctx context.Context,
	metadata *engine.ClientMetadata,
	parentClientID string,
	attachablesClientID string,
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
	_, recordExists := sess.clientRecords[metadata.ClientID]
	_, runtimeExists := sess.clientRuntimes[metadata.ClientID]
	parentRecord := sess.clientRecords[parentClientID]
	parent := sess.clientRuntimes[parentClientID]
	sess.clientMu.RUnlock()
	if recordExists || runtimeExists {
		return nil, fmt.Errorf("nested client %q transport is already registered or permanently closed", metadata.ClientID)
	}
	if parentRecord == nil || parent == nil || parent.clientRecord != parentRecord {
		return nil, fmt.Errorf("parent client %q not found for nested client %q", parentClientID, metadata.ClientID)
	}
	parentIDs, err := sess.parentClientIDsForRegistration(metadata.ClientID, parentClientID, false)
	if err != nil {
		return nil, fmt.Errorf("validate client ancestry: %w", err)
	}
	attachablesClientID, err = sess.validateNestedTransportAttachables(metadata, parentIDs, attachablesClientID)
	if err != nil {
		return nil, err
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
	record := &clientRecord{
		daggerSession:       sess,
		clientID:            metadata.ClientID,
		clientVersion:       metadata.ClientVersion,
		secretToken:         metadata.ClientSecretToken,
		shutdownCh:          make(chan struct{}),
		clientMetadata:      metadataSnapshot,
		parentClientIDs:     parentIDs,
		attachablesClientID: attachablesClientID,
		accepting:           true,
	}
	client := &clientRuntime{
		clientRecord:           record,
		state:                  clientStateUninitialized,
		lifecycleLeases:        make(map[uint64]clientLifecycleLeaseRecord),
		parentClientScopeLease: parentScope.Lease(),
	}
	transport := engine.NewNestedClientTransport(func() {
		sess.closeNestedClientTransport(record)
	})
	client.nestedTransport = transport

	sess.scopeMu.Lock()
	client.transportLease = sess.newClientLifecycleLeaseLocked(client, engine.ClientLeaseTransport, metadata.ClientID)
	sess.scopeMu.Unlock()

	sess.clientMu.Lock()
	if _, duplicate := sess.clientRecords[metadata.ClientID]; duplicate {
		sess.clientMu.Unlock()
		client.transportLease.Release()
		return nil, fmt.Errorf("nested client %q transport was concurrently registered", metadata.ClientID)
	}
	if _, duplicate := sess.clientRuntimes[metadata.ClientID]; duplicate {
		sess.clientMu.Unlock()
		client.transportLease.Release()
		return nil, fmt.Errorf("nested client %q runtime was concurrently registered", metadata.ClientID)
	}
	sess.clientRecords[metadata.ClientID] = record
	sess.clientRuntimes[metadata.ClientID] = client
	sess.clientMu.Unlock()
	return transport, nil
}

// validateNestedTransportAttachables seals a logical nested client to the
// bootstrap attachables channel created by the same exec. An ordinary nested
// client owns its own channel and therefore needs no cross-record validation.
func (sess *daggerSession) validateNestedTransportAttachables(
	metadata *engine.ClientMetadata,
	parentIDs []string,
	attachablesClientID string,
) (string, error) {
	if attachablesClientID == "" {
		return metadata.ClientID, nil
	}
	if attachablesClientID == metadata.ClientID {
		return attachablesClientID, nil
	}

	sess.clientMu.RLock()
	attachablesRecord := sess.clientRecords[attachablesClientID]
	sess.clientMu.RUnlock()
	if attachablesRecord == nil {
		return "", fmt.Errorf("attachables client %q not found for nested client %q", attachablesClientID, metadata.ClientID)
	}
	if attachablesRecord.nestedTransport == nil || attachablesRecord.nestedTransport.Closed() {
		return "", fmt.Errorf("attachables client %q is closed for nested client %q", attachablesClientID, metadata.ClientID)
	}
	if attachablesRecord.attachablesClientID != "" && attachablesRecord.attachablesClientID != attachablesRecord.clientID {
		return "", fmt.Errorf("attachables client %q is not an exec bootstrap client", attachablesClientID)
	}
	if attachablesRecord.secretToken != metadata.ClientSecretToken {
		return "", fmt.Errorf("attachables client %q has a different secret token", attachablesClientID)
	}
	if !slices.Equal(attachablesRecord.parentClientIDs, parentIDs) {
		return "", fmt.Errorf("attachables client %q is outside nested client %q's exec scope", attachablesClientID, metadata.ClientID)
	}
	return attachablesClientID, nil
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
func (sess *daggerSession) mergeClientMetadataLocked(record *clientRecord, incoming *engine.ClientMetadata) error {
	if incoming == nil {
		return errors.New("client metadata is nil")
	}
	candidate, err := cloneClientMetadata(incoming)
	if err != nil {
		return err
	}
	if record.clientMetadata == nil {
		if record.metadataSealed {
			return fmt.Errorf("client %q has sealed empty metadata", record.clientID)
		}
		record.clientMetadata = candidate
		return nil
	}

	stored := reflect.ValueOf(record.clientMetadata).Elem()
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
			if record.metadataSealed {
				return fmt.Errorf("client %q metadata is sealed; field %s cannot be completed", record.clientID, fieldName)
			}
			storedField.Set(incomingField)
			continue
		}
		if !reflect.DeepEqual(storedField.Interface(), incomingField.Interface()) {
			return fmt.Errorf("client %q metadata field %s conflicts with bootstrap value", record.clientID, fieldName)
		}
	}
	return nil
}

// sealClientMetadataLocked freezes one authoritative snapshot. Re-cloning at
// the boundary ensures no alias retained by an earlier bootstrap caller can
// mutate the value later. sess.scopeMu must be held.
func (sess *daggerSession) sealClientMetadataLocked(record *clientRecord) error {
	if record.metadataSealed {
		return nil
	}
	if record.clientMetadata == nil {
		return fmt.Errorf("client %q has no metadata to seal", record.clientID)
	}
	if !record.accepting {
		return fmt.Errorf("client %q is closed", record.clientID)
	}
	if record.nestedTransport != nil && record.nestedTransport.Closed() {
		return fmt.Errorf("nested client %q transport is closed", record.clientID)
	}
	snapshot, err := cloneClientMetadata(record.clientMetadata)
	if err != nil {
		return err
	}
	record.clientMetadata = snapshot
	record.clientVersion = snapshot.ClientVersion
	record.secretToken = snapshot.ClientSecretToken
	record.metadataSealed = true
	return nil
}

// clientMetadataSnapshot returns an independent copy of sealed metadata for
// lookups outside the runtime. Callers can mutate it without changing future
// scopes or lookups.
func (sess *daggerSession) clientMetadataSnapshot(record *clientRecord) (*engine.ClientMetadata, error) {
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	if !record.metadataSealed {
		return nil, fmt.Errorf("client %q metadata is not sealed", record.clientID)
	}
	return cloneClientMetadata(record.clientMetadata)
}

func (sess *daggerSession) closeNestedClientTransport(record *clientRecord) {
	var transport *engine.ClientLifecycleLease
	sess.scopeMu.Lock()
	sess.markClientClosedLocked(record)
	sess.clientMu.RLock()
	client := sess.clientRuntimes[record.clientID]
	sess.clientMu.RUnlock()
	if client != nil && client.clientRecord == record {
		transport = client.transportLease
		client.transportLease = nil
	}
	reclamation := sess.maybeBeginClientRuntimeReclamationLocked(client)
	sess.scopeMu.Unlock()

	transport.Release()
	sess.finishClientRuntimeReclamation(reclamation)
}

// markClientClosedLocked performs the monotonic reachability transition. The
// session scope lock is the acceptance serialization point.
func (sess *daggerSession) markClientClosedLocked(record *clientRecord) {
	if record == nil || !record.accepting {
		return
	}
	record.accepting = false
	record.closedAt = time.Now()
}

type clientRuntimeReclamation struct {
	runtime *clientRuntime
}

// maybeBeginClientRuntimeReclamationLocked starts one serialized quiescent transition.
// sess.scopeMu must be held. The closed runtime remains published but cannot
// admit or execute work while its metric provider performs its final drain.
func (sess *daggerSession) maybeBeginClientRuntimeReclamationLocked(client *clientRuntime) clientRuntimeReclamation {
	if client == nil || sess.state.Load() != sessionStateInitialized || client.accepting || len(client.lifecycleLeases) != 0 || client.reclaiming {
		return clientRuntimeReclamation{}
	}

	sess.clientMu.RLock()
	if sess.clientRuntimes[client.clientID] != client {
		sess.clientMu.RUnlock()
		return clientRuntimeReclamation{}
	}
	sess.clientMu.RUnlock()
	client.reclaiming = true

	return clientRuntimeReclamation{runtime: client}
}

// finishClientRuntimeReclamation runs outside session locks. Heavy state is
// unpublished only after metrics finish draining, then dropped before releasing
// the parent's child lease; that release may recursively reclaim a closed parent.
func (sess *daggerSession) finishClientRuntimeReclamation(reclamation clientRuntimeReclamation) {
	if reclamation.runtime == nil {
		return
	}
	metricsCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	if err := reclamation.runtime.shutdownMetrics(metricsCtx); err != nil {
		slog.Error("error shutting down quiescent client metrics",
			"sessionID", sess.sessionID,
			"clientID", reclamation.runtime.clientID,
			"error", err)
	}
	cancel()

	sess.scopeMu.Lock()
	sess.clientMu.Lock()
	if sess.clientRuntimes[reclamation.runtime.clientID] == reclamation.runtime {
		delete(sess.clientRuntimes, reclamation.runtime.clientID)
		reclamation.runtime.quiescentAt = time.Now()
	}
	parentLease := reclamation.runtime.parentClientScopeLease
	reclamation.runtime.parentClientScopeLease = nil
	sess.clientMu.Unlock()
	sess.scopeMu.Unlock()

	reclamation.runtime.releaseHeavyState()
	parentLease.Release()
}

// acquireRootClientScope acquires work directly from a reachable client. Root
// acquisition is rejected after client close or session teardown; background
// work must clone an already-held scope instead.
func (sess *daggerSession) acquireRootClientScope(
	client *clientRuntime,
	kind engine.ClientLeaseKind,
	ownerID string,
) (engine.ClientScope, error) {
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()

	if sess.state.Load() != sessionStateInitialized {
		return engine.ClientScope{}, fmt.Errorf("session %q is not initialized", sess.sessionID)
	}
	sess.clientMu.RLock()
	published := sess.clientRuntimes[client.clientID] == client
	sess.clientMu.RUnlock()
	if !published {
		return engine.ClientScope{}, fmt.Errorf("client runtime %q is not retained", client.clientID)
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
	client *clientRuntime,
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
	client *clientRuntime,
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
	client *clientRuntime,
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

func (sess *daggerSession) releaseClientLifecycleLease(client *clientRuntime, leaseID uint64) {
	sess.scopeMu.Lock()
	delete(client.lifecycleLeases, leaseID)
	reclamation := sess.maybeBeginClientRuntimeReclamationLocked(client)
	sess.signalClientScopeWaiterLocked()
	sess.scopeMu.Unlock()
	sess.finishClientRuntimeReclamation(reclamation)
}

func (sess *daggerSession) signalClientScopeWaiterLocked() {
	if sess.scopeWaitCh != nil {
		close(sess.scopeWaitCh)
		sess.scopeWaitCh = nil
	}
}

// clientScopesDrainedLocked reports whether session teardown has observed the
// terminal transition of every executable client scope. sess.scopeMu must be
// held; the lock order matches maybeBeginClientRuntimeReclamationLocked.
func (sess *daggerSession) clientScopesDrainedLocked() bool {
	sess.clientMu.RLock()
	defer sess.clientMu.RUnlock()
	for _, client := range sess.clientRuntimes {
		if len(client.lifecycleLeases) != 0 {
			return false
		}
	}
	return true
}

// waitForClientScopeDrain is the session-level producer barrier. Teardown first
// prevents acquisition, then services, requests, and cache callbacks release
// their existing leases through normal terminal transitions.
func (sess *daggerSession) waitForClientScopeDrain(ctx context.Context) error {
	for {
		sess.scopeMu.Lock()
		if sess.clientScopesDrainedLocked() {
			sess.scopeMu.Unlock()
			return nil
		}
		if sess.scopeWaitCh == nil {
			sess.scopeWaitCh = make(chan struct{})
		}
		waitCh := sess.scopeWaitCh
		sess.scopeMu.Unlock()

		select {
		case <-waitCh:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// closeClientScope closes root acquisition and releases reachability. Existing
// scopes remain cloneable until their own leases are released.
func (sess *daggerSession) closeClientScope(client *clientRuntime) {
	sess.scopeMu.Lock()
	sess.markClientClosedLocked(client.clientRecord)
	transport := client.transportLease
	client.transportLease = nil
	reclamation := sess.maybeBeginClientRuntimeReclamationLocked(client)
	sess.scopeMu.Unlock()

	transport.Release()
	sess.finishClientRuntimeReclamation(reclamation)
}

// markSessionRemoved gives authoritative teardown the same serialization point
// as acquisition and reclamation: whichever takes scopeMu first owns the final
// live-session transition.
func (sess *daggerSession) markSessionRemoved() {
	sess.scopeMu.Lock()
	sess.state.Store(sessionStateRemoved)
	sess.scopeMu.Unlock()
}

// beginClientScopeTeardown prevents every client from accepting new root work.
// Existing leased work drains through its normal terminal transitions.
func (sess *daggerSession) beginClientScopeTeardown() {
	sess.clientMu.RLock()
	clients := make([]*clientRuntime, 0, len(sess.clientRuntimes))
	for _, client := range sess.clientRuntimes {
		clients = append(clients, client)
	}
	sess.clientMu.RUnlock()

	var transports []*engine.ClientLifecycleLease
	var parentScopes []*engine.ClientLifecycleLease
	sess.scopeMu.Lock()
	for _, client := range clients {
		sess.markClientClosedLocked(client.clientRecord)
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
	// Session teardown is authoritative and disables independent reclamation by
	// publishing sessionStateRemoved first. Release any remaining child edges
	// here so teardown cannot strand a typed lease even if a child never reached
	// its live-session quiescent transition.
	for _, parentScope := range parentScopes {
		parentScope.Release()
	}
}

func (sess *daggerSession) clientMetadataSealed(record *clientRecord) bool {
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	return record.metadataSealed
}

func (sess *daggerSession) clientLifecycleSnapshot(client *clientRuntime) (bool, bool, []clientLifecycleLeaseRecord) {
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	out := make([]clientLifecycleLeaseRecord, 0, len(client.lifecycleLeases))
	for _, lease := range client.lifecycleLeases {
		out = append(out, lease)
	}
	return client.accepting, client.lifecycleLeases != nil, out
}
