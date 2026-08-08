package server

import "sync"

const (
	sessionHookSessionPublished               = "session_published"
	sessionHookCreatorAttachablesRegistration = "creator_attachables_registration"
	sessionHookObserverBeforeValidation       = "observer_before_validation"
	sessionHookObserverAfterValidation        = "observer_after_validation"
	sessionHookAttachmentPublished            = "attachment_published"
	sessionHookSourcePublicationRead          = "source_publication_read"
	sessionHookSourceReservation              = "source_reservation"
	sessionHookExplicitClose                  = "explicit_close"
	sessionHookAttachmentDeath                = "attachment_death"
	sessionHookAttachmentCleared              = "attachment_cleared"
	sessionHookObserverCleanup                = "observer_cleanup"
	sessionHookQueryBodyCommitted             = "query_body_committed"
	sessionHookQueryGoroutineStarted          = "query_goroutine_started"
	sessionHookGraphQLEntered                 = "graphql_entered"
	sessionHookResultBodyCommitted            = "result_body_committed"
	sessionHookCreatorTelemetryFlush          = "creator_telemetry_flush"
	sessionHookCompletionCursorCapture        = "completion_cursor_capture"
	sessionHookSSEFinalRow                    = "sse_final_row"
	sessionHookQueryStatusCompleted           = "query_status_completed"
	sessionHookTerminalTransition             = "terminal_transition"
	sessionHookResourceCleanup                = "resource_cleanup"
	sessionHookRegistryDelete                 = "registry_delete"
)

type sessionTestEvent struct {
	Name         string
	SessionID    string
	ClientID     string
	AttachmentID string
	Generation   uint64
	Resource     string
}

// sessionTestHooks is package-private deterministic fault injection. Production
// servers leave it nil. Tests may observe events and install channel barriers at
// exact lifecycle boundaries without relying on scheduler timing.
type sessionTestHooks struct {
	Events chan sessionTestEvent

	mu       sync.RWMutex
	barriers map[string]<-chan struct{}
}

func (hooks *sessionTestHooks) setBarrier(name string, barrier <-chan struct{}) {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if hooks.barriers == nil {
		hooks.barriers = map[string]<-chan struct{}{}
	}
	hooks.barriers[name] = barrier
}

func (srv *Server) hitSessionHook(event sessionTestEvent) {
	hooks := srv.sessionTestHooks
	if hooks == nil {
		return
	}
	if hooks.Events != nil {
		hooks.Events <- event
	}
	hooks.mu.RLock()
	barrier := hooks.barriers[event.Name]
	hooks.mu.RUnlock()
	if barrier != nil {
		<-barrier
	}
}
