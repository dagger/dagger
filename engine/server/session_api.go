package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

// EngineSessions lists the initialized sessions on this engine, oldest first,
// each with the clients that connected directly to it.
func (srv *Server) EngineSessions(ctx context.Context) ([]*core.EngineSession, error) {
	srv.daggerSessionsMu.RLock()
	sessions := make([]*daggerSession, 0, len(srv.daggerSessions))
	for _, sess := range srv.daggerSessions {
		sessions = append(sessions, sess)
	}
	srv.daggerSessionsMu.RUnlock()
	slices.SortFunc(sessions, func(a, b *daggerSession) int {
		return cmp.Or(a.createdAt.Compare(b.createdAt), cmp.Compare(a.sessionID, b.sessionID))
	})

	var out []*core.EngineSession
	for _, sess := range sessions {
		if sess.state.Load() != sessionStateInitialized {
			continue
		}
		clients, err := sess.engineSessionClients()
		if err != nil {
			return nil, fmt.Errorf("session %q: %w", sess.sessionID, err)
		}
		out = append(out, &core.EngineSession{
			SessionID:  sess.sessionID,
			Detached:   sess.detached,
			CreatedAt:  sess.createdAt.UTC().Format(time.RFC3339),
			ClientList: clients,
		})
	}
	return out, nil
}

// engineSessionClients describes the session's root clients, oldest first.
func (sess *daggerSession) engineSessionClients() ([]*core.EngineSessionClient, error) {
	sess.clientMu.RLock()
	var roots []*clientRecord
	runtimes := map[string]*clientRuntime{}
	for id, record := range sess.clientRecords {
		if len(record.parentClientIDs) == 0 {
			roots = append(roots, record)
			runtimes[id] = sess.clientRuntimes[id]
		}
	}
	sess.clientMu.RUnlock()
	slices.SortFunc(roots, func(a, b *clientRecord) int {
		return cmp.Compare(a.seq, b.seq)
	})

	running := sess.services.RunningServices(sess.sessionID)
	var out []*core.EngineSessionClient
	for _, record := range roots {
		sess.scopeMu.Lock()
		md := record.clientMetadata
		var err error
		if md != nil {
			md, err = cloneClientMetadata(md)
		}
		sess.scopeMu.Unlock()
		if err != nil {
			return nil, err
		}
		if md == nil {
			continue
		}
		client := &core.EngineSessionClient{
			ClientID:   record.clientID,
			Hostname:   md.ClientHostname,
			Background: md.Background,
			Provides:   []string{},
			SessionID:  sess.sessionID,
		}
		if md.PID != 0 {
			client.PID = dagql.NonNull(dagql.NewInt(md.PID))
		}
		if md.Command != "" {
			client.Command = dagql.NonNull(dagql.NewString(md.Command))
		}
		if caller := sess.attachables.lookup(record.clientID); caller != nil {
			client.Connected = true
			client.Provides = attachableKindNames(caller)
		}
		// Tunnels are the client-specific shared services a client starts.
		for _, svc := range running {
			if svc.Key.ClientID == record.clientID && svc.Key.Kind == core.ServiceRuntimeShared {
				client.ForwardList = append(client.ForwardList, svc.Ports...)
			}
		}
		if runtime := runtimes[record.clientID]; runtime != nil && runtime.workspace != nil {
			client.Workspace = dagql.NonNull(dagql.NewString(runtime.workspace.Address))
		}
		out = append(out, client)
	}
	return out, nil
}

// attachableKindNames returns the sorted kind names of the services a caller
// serves.
func attachableKindNames(caller *sessionAttachableCaller) []string {
	names := []string{}
	for service, kind := range attachableKinds {
		if caller.Provides(service) && !slices.Contains(names, kind.name) {
			names = append(names, kind.name)
		}
	}
	slices.Sort(names)
	return names
}

func (srv *Server) initializedSession(sessionID string) (*daggerSession, error) {
	srv.daggerSessionsMu.RLock()
	sess := srv.daggerSessions[sessionID]
	srv.daggerSessionsMu.RUnlock()
	if sess == nil || sess.state.Load() != sessionStateInitialized {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	return sess, nil
}

// StopEngineSession schedules teardown of a session and returns without
// waiting for it. Teardown waits for the session's in-flight requests, which
// include this one when a session stops itself.
func (srv *Server) StopEngineSession(ctx context.Context, sessionID string) error {
	sess, err := srv.initializedSession(sessionID)
	if err != nil {
		return err
	}
	go srv.stopDaggerSession(context.WithoutCancel(ctx), sess)
	return nil
}

func (srv *Server) stopDaggerSession(ctx context.Context, sess *daggerSession) {
	sess.lifecycleMu.Lock()
	if sess.state.Load() != sessionStateInitialized {
		// Already removed by another stop or a reap.
		sess.lifecycleMu.Unlock()
		return
	}
	slog.Info("stopping session", "sessionID", sess.sessionID)
	err := srv.removeDaggerSession(ctx, sess)
	sess.lifecycleMu.Unlock()
	srv.retireSession(sess)
	if err != nil {
		slog.Error("session teardown failed",
			"sessionID", sess.sessionID,
			"error", err,
		)
	}
}

// CloseEngineSessionClient closes a root client of a session and its
// attachables connection. The client cannot make further requests; its
// pending lockfile changes are not written.
func (srv *Server) CloseEngineSessionClient(ctx context.Context, sessionID, clientID string) error {
	sess, err := srv.initializedSession(sessionID)
	if err != nil {
		return err
	}
	sess.clientMu.RLock()
	record := sess.clientRecords[clientID]
	runtime := sess.clientRuntimes[clientID]
	sess.clientMu.RUnlock()
	if record == nil || len(record.parentClientIDs) != 0 {
		return fmt.Errorf("client %q not found", clientID)
	}
	slog.Info("closing session client", "sessionID", sessionID, "clientID", clientID)
	if runtime != nil {
		sess.closeClientScope(runtime)
	} else {
		sess.scopeMu.Lock()
		sess.markClientClosedLocked(record)
		sess.scopeMu.Unlock()
	}
	sess.attachables.Close(clientID, errors.New("client closed through the engine API"))
	return nil
}
