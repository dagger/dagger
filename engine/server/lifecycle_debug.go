package server

import (
	"sort"
	"time"
)

// LifecycleDebugSnapshot is a point-in-time view of the current
// daggerClient lifecycle. It deliberately describes the pre-reclamation model:
// one retained record and one retained runtime per published client.
type LifecycleDebugSnapshot struct {
	GeneratedAt         time.Time                       `json:"generated_at"`
	Records             int                             `json:"records"`
	Runtimes            int                             `json:"runtimes"`
	ClosedRuntimes      int                             `json:"closed_runtimes"`
	OldestClosedRuntime *time.Time                      `json:"oldest_closed_runtime,omitempty"`
	ActiveRequests      int                             `json:"active_requests"`
	OpenClientDBs       int                             `json:"open_client_dbs"`
	OpenClientDBStreams int                             `json:"open_client_db_streams"`
	OpenClientDBRefs    int                             `json:"open_client_db_refs"`
	Providers           LifecycleTelemetryCounts        `json:"providers"`
	Sessions            []SessionLifecycleDebugSnapshot `json:"sessions"`
}

// LifecycleTelemetryCounts reports configured provider, processor, reader, and
// queue cardinality. Trace/log counts are session-owned; metric counts remain
// per runtime. Queue capacity is configured fact, never measured occupancy.
type LifecycleTelemetryCounts struct {
	TracerProviders          int `json:"tracer_providers"`
	LoggerProviders          int `json:"logger_providers"`
	MeterProviders           int `json:"meter_providers"`
	ConfiguredSpanProcessors int `json:"configured_span_processors"`
	ConfiguredLogProcessors  int `json:"configured_log_processors"`
	ConfiguredMetricReaders  int `json:"configured_metric_readers"`
	ConfiguredSpanQueueSlots int `json:"configured_span_queue_slots"`
	ConfiguredLogQueueSlots  int `json:"configured_log_queue_slots"`
	// The OTel batch processors do not expose queue occupancy. Keep this
	// explicit so configured capacity is never mistaken for measured backlog.
	QueueOccupancyMeasured bool `json:"queue_occupancy_measured"`
}

type SessionLifecycleDebugSnapshot struct {
	SessionID string                         `json:"session_id"`
	State     string                         `json:"state"`
	Records   int                            `json:"records"`
	Runtimes  int                            `json:"runtimes"`
	Telemetry LifecycleTelemetryCounts       `json:"telemetry"`
	Clients   []ClientLifecycleDebugSnapshot `json:"clients"`
}

type ClientLifecycleDebugSnapshot struct {
	ClientID         string                     `json:"client_id"`
	ParentIDs        []string                   `json:"parent_ids,omitempty"`
	RecordState      string                     `json:"record_state"`
	RuntimeState     string                     `json:"runtime_state"`
	MetadataSealed   bool                       `json:"metadata_sealed"`
	ActiveRequests   int                        `json:"active_requests"`
	ShutdownAt       *time.Time                 `json:"shutdown_at,omitempty"`
	RetentionReasons []LifecycleRetentionReason `json:"retention_reasons,omitempty"`
	Telemetry        LifecycleTelemetryCounts   `json:"telemetry"`
}

type LifecycleRetentionReason struct {
	Kind    string `json:"kind"`
	OwnerID string `json:"owner_id,omitempty"`
	Count   int    `json:"count,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// ClientLifecycleDebugSnapshot reports why current clients remain retained.
// It is lock-safe for use from the engine debug server while sessions initialize
// or tear down and does not acquire a session lifecycleMu.
func (srv *Server) ClientLifecycleDebugSnapshot() LifecycleDebugSnapshot {
	now := time.Now()
	out := LifecycleDebugSnapshot{GeneratedAt: now}
	if srv.clientDBs != nil {
		stats := srv.clientDBs.OpenStats()
		out.OpenClientDBs = stats.Stores
		out.OpenClientDBStreams = stats.Streams
		out.OpenClientDBRefs = stats.Refs
	}

	srv.daggerSessionsMu.RLock()
	sessions := make([]*daggerSession, 0, len(srv.daggerSessions))
	for _, sess := range srv.daggerSessions {
		sessions = append(sessions, sess)
	}
	srv.daggerSessionsMu.RUnlock()

	for _, sess := range sessions {
		sessOut := SessionLifecycleDebugSnapshot{
			SessionID: sess.sessionID,
			State:     sess.state.Load().String(),
			Telemetry: sess.telemetryDebug,
		}
		addLifecycleTelemetryCounts(&out.Providers, sessOut.Telemetry)

		sess.clientMu.RLock()
		clients := make([]*daggerClient, 0, len(sess.clients))
		for _, client := range sess.clients {
			clients = append(clients, client)
		}
		sess.clientMu.RUnlock()

		descendants := make(map[string]int, len(clients))
		for _, client := range clients {
			for _, parentID := range client.parentClientIDs {
				descendants[parentID]++
			}
		}

		for _, client := range clients {
			client.stateMu.RLock()
			clientOut := ClientLifecycleDebugSnapshot{
				ClientID:       client.clientID,
				MetadataSealed: false,
				ActiveRequests: client.activeCount,
			}
			initialized := client.state == clientStateInitialized
			shutdownAt := client.shutdownAt
			client.stateMu.RUnlock()

			clientOut.ParentIDs = append(clientOut.ParentIDs, client.parentClientIDs...)

			if shutdownAt.IsZero() {
				clientOut.RecordState = "open-or-unobserved"
			} else {
				closed := shutdownAt
				clientOut.RecordState = "shutdown-signaled"
				clientOut.ShutdownAt = &closed
				out.ClosedRuntimes++
				if out.OldestClosedRuntime == nil || closed.Before(*out.OldestClosedRuntime) {
					out.OldestClosedRuntime = &closed
				}
			}

			switch {
			case !initialized:
				clientOut.RuntimeState = "initializing"
			case shutdownAt.IsZero():
				clientOut.RuntimeState = "open"
			default:
				clientOut.RuntimeState = "closed-retained"
				clientOut.RetentionReasons = append(clientOut.RetentionReasons, LifecycleRetentionReason{
					Kind:   "session-lifetime",
					Detail: "runtime reclamation is not enabled",
				})
			}
			if clientOut.ActiveRequests > 0 {
				clientOut.RetentionReasons = append(clientOut.RetentionReasons, LifecycleRetentionReason{
					Kind:    "request",
					OwnerID: client.clientID,
					Count:   clientOut.ActiveRequests,
				})
			}
			if n := descendants[client.clientID]; n > 0 {
				clientOut.RetentionReasons = append(clientOut.RetentionReasons, LifecycleRetentionReason{
					Kind:    "descendant",
					OwnerID: client.clientID,
					Count:   n,
				})
			}
			clientOut.Telemetry = client.telemetryDebug

			sessOut.Clients = append(sessOut.Clients, clientOut)
			sessOut.Records++
			sessOut.Runtimes++
			out.Records++
			out.Runtimes++
			out.ActiveRequests += clientOut.ActiveRequests
			addLifecycleTelemetryCounts(&out.Providers, clientOut.Telemetry)
		}

		sort.Slice(sessOut.Clients, func(i, j int) bool {
			return sessOut.Clients[i].ClientID < sessOut.Clients[j].ClientID
		})
		out.Sessions = append(out.Sessions, sessOut)
	}

	sort.Slice(out.Sessions, func(i, j int) bool {
		return out.Sessions[i].SessionID < out.Sessions[j].SessionID
	})
	return out
}

func addLifecycleTelemetryCounts(dst *LifecycleTelemetryCounts, src LifecycleTelemetryCounts) {
	dst.TracerProviders += src.TracerProviders
	dst.LoggerProviders += src.LoggerProviders
	dst.MeterProviders += src.MeterProviders
	dst.ConfiguredSpanProcessors += src.ConfiguredSpanProcessors
	dst.ConfiguredLogProcessors += src.ConfiguredLogProcessors
	dst.ConfiguredMetricReaders += src.ConfiguredMetricReaders
	dst.ConfiguredSpanQueueSlots += src.ConfiguredSpanQueueSlots
	dst.ConfiguredLogQueueSlots += src.ConfiguredLogQueueSlots
	// Occupancy is measured only when every contributing runtime can report it.
	dst.QueueOccupancyMeasured = dst.QueueOccupancyMeasured && src.QueueOccupancyMeasured
}
