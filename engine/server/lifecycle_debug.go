package server

import (
	"sort"
	"time"
)

// LifecycleDebugSnapshot is a point-in-time view of session-long client records
// and the execution runtimes that remain published by typed leases.
type LifecycleDebugSnapshot struct {
	GeneratedAt         time.Time                       `json:"generated_at"`
	Records             int                             `json:"records"`
	Runtimes            int                             `json:"runtimes"`
	ClosedRuntimes      int                             `json:"closed_runtimes"`
	OldestClosedRuntime *time.Time                      `json:"oldest_closed_runtime,omitempty"`
	ActiveRequests      int                             `json:"active_requests"`
	LeaseCounts         []LifecycleRetentionReason      `json:"lease_counts,omitempty"`
	OpenClientDBs       int                             `json:"open_client_dbs"`
	OpenClientDBStreams int                             `json:"open_client_db_streams"`
	OpenClientDBRefs    int                             `json:"open_client_db_refs"`
	Providers           LifecycleTelemetryCounts        `json:"providers"`
	Sessions            []SessionLifecycleDebugSnapshot `json:"sessions"`
}

// LifecycleTelemetryCounts reports configured provider, processor, reader, and
// queue cardinality. All provider and reader counts are session-owned. Queue
// capacity is configured fact, never measured occupancy.
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
	SessionID   string                         `json:"session_id"`
	State       string                         `json:"state"`
	Records     int                            `json:"records"`
	Runtimes    int                            `json:"runtimes"`
	LeaseCounts []LifecycleRetentionReason     `json:"lease_counts,omitempty"`
	Telemetry   LifecycleTelemetryCounts       `json:"telemetry"`
	Clients     []ClientLifecycleDebugSnapshot `json:"clients"`
}

type ClientLifecycleDebugSnapshot struct {
	ClientID         string                     `json:"client_id"`
	ParentIDs        []string                   `json:"parent_ids,omitempty"`
	RecordState      string                     `json:"record_state"`
	RuntimeState     string                     `json:"runtime_state"`
	MetadataSealed   bool                       `json:"metadata_sealed"`
	ActiveRequests   int                        `json:"active_requests"`
	ClosedAt         *time.Time                 `json:"closed_at,omitempty"`
	QuiescentAt      *time.Time                 `json:"quiescent_at,omitempty"`
	ShutdownAt       *time.Time                 `json:"shutdown_at,omitempty"`
	RetentionReasons []LifecycleRetentionReason `json:"retention_reasons,omitempty"`
	// Telemetry is retained for debug API compatibility and is now always zero;
	// provider topology lives on the owning session snapshot.
	Telemetry LifecycleTelemetryCounts `json:"telemetry"`
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
//
//nolint:gocyclo // debug snapshot enumerates every lifecycle dimension in one lock-safe pass
func (srv *Server) ClientLifecycleDebugSnapshot() LifecycleDebugSnapshot {
	now := time.Now()
	out := LifecycleDebugSnapshot{GeneratedAt: now}
	allLeaseCounts := make(map[string]LifecycleRetentionReason)
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
		sessionLeaseCounts := make(map[string]LifecycleRetentionReason)
		sessOut := SessionLifecycleDebugSnapshot{
			SessionID: sess.sessionID,
			State:     sess.state.Load().String(),
			Telemetry: sess.telemetryDebug,
		}
		addLifecycleTelemetryCounts(&out.Providers, sessOut.Telemetry)

		sess.clientMu.RLock()
		records := make([]*clientRecord, 0, len(sess.clientRecords))
		runtimes := make(map[string]*clientRuntime, len(sess.clientRuntimes))
		for _, record := range sess.clientRecords {
			records = append(records, record)
		}
		for id, runtime := range sess.clientRuntimes {
			runtimes[id] = runtime
		}
		sess.clientMu.RUnlock()

		for _, record := range records {
			runtime := runtimes[record.clientID]
			var (
				lifecycleTracked bool
				leases           []clientLifecycleLeaseRecord
				activeCount      int
				initialized      bool
			)
			if runtime != nil {
				_, lifecycleTracked, leases = sess.clientLifecycleSnapshot(runtime)
				runtime.stateMu.RLock()
				activeCount = runtime.activeCount
				initialized = runtime.state == clientStateInitialized
				runtime.stateMu.RUnlock()
			}
			clientLeaseCounts := make(map[string]LifecycleRetentionReason)
			for _, lease := range leases {
				addLifecycleLeaseCount(clientLeaseCounts, string(lease.kind), lease.ownerID)
				addLifecycleLeaseCount(sessionLeaseCounts, string(lease.kind), lease.ownerID)
				addLifecycleLeaseCount(allLeaseCounts, string(lease.kind), lease.ownerID)
			}

			sess.scopeMu.Lock()
			accepting := record.accepting
			metadataSealed := record.metadataSealed
			closedAt := record.closedAt
			quiescentAt := record.quiescentAt
			shutdownAt := record.shutdownAt
			sess.scopeMu.Unlock()
			clientOut := ClientLifecycleDebugSnapshot{
				ClientID:       record.clientID,
				MetadataSealed: metadataSealed,
			}
			requestLeases := lifecycleLeaseCountKind(clientLeaseCounts, "request")
			if requestLeases > 0 {
				clientOut.ActiveRequests = requestLeases
			} else {
				clientOut.ActiveRequests = activeCount
			}

			clientOut.ParentIDs = append(clientOut.ParentIDs, record.parentClientIDs...)
			if runtime != nil && !lifecycleTracked {
				// Compatibility for synthetic test/debug runtimes created before typed
				// lease tracking. Production runtimes always have a lease map.
				accepting = shutdownAt.IsZero() && closedAt.IsZero()
			}

			if !closedAt.IsZero() {
				closed := closedAt
				clientOut.ClosedAt = &closed
			}
			if !quiescentAt.IsZero() {
				quiescent := quiescentAt
				clientOut.QuiescentAt = &quiescent
			}
			if !shutdownAt.IsZero() {
				shutdown := shutdownAt
				clientOut.ShutdownAt = &shutdown
			}

			if accepting {
				clientOut.RecordState = "open"
			} else if !shutdownAt.IsZero() {
				clientOut.RecordState = "shutdown-signaled"
			} else {
				clientOut.RecordState = "closed"
			}
			if runtime != nil && !accepting {
				out.ClosedRuntimes++
				oldest := closedAt
				if oldest.IsZero() {
					oldest = shutdownAt
				}
				if !oldest.IsZero() && (out.OldestClosedRuntime == nil || oldest.Before(*out.OldestClosedRuntime)) {
					out.OldestClosedRuntime = &oldest
				}
			}

			switch {
			case runtime == nil && !quiescentAt.IsZero():
				clientOut.RuntimeState = "quiescent"
			case runtime == nil:
				clientOut.RuntimeState = "not-retained"
			case !initialized:
				clientOut.RuntimeState = "initializing"
			case accepting:
				clientOut.RuntimeState = "open"
			default:
				clientOut.RuntimeState = "closed-retained"
				if len(clientLeaseCounts) == 0 {
					detail := "runtime is awaiting its serialized reclamation transition"
					if sess.state.Load() == sessionStateRemoved {
						detail = "authoritative session teardown owns final cleanup"
					}
					clientOut.RetentionReasons = append(clientOut.RetentionReasons, LifecycleRetentionReason{
						Kind:   "lifecycle-transition",
						Detail: detail,
					})
				}
			}
			clientOut.RetentionReasons = append(clientOut.RetentionReasons, sortedLifecycleLeaseCounts(clientLeaseCounts)...)
			if clientOut.ActiveRequests > 0 && requestLeases == 0 {
				clientOut.RetentionReasons = append(clientOut.RetentionReasons, LifecycleRetentionReason{
					Kind:    "request",
					OwnerID: record.clientID,
					Count:   clientOut.ActiveRequests,
				})
			}

			sessOut.Clients = append(sessOut.Clients, clientOut)
			sessOut.Records++
			out.Records++
			if runtime != nil {
				sessOut.Runtimes++
				out.Runtimes++
			}
			out.ActiveRequests += clientOut.ActiveRequests
			addLifecycleTelemetryCounts(&out.Providers, clientOut.Telemetry)
		}

		sessOut.LeaseCounts = sortedLifecycleLeaseCounts(sessionLeaseCounts)
		sort.Slice(sessOut.Clients, func(i, j int) bool {
			return sessOut.Clients[i].ClientID < sessOut.Clients[j].ClientID
		})
		out.Sessions = append(out.Sessions, sessOut)
	}

	out.LeaseCounts = sortedLifecycleLeaseCounts(allLeaseCounts)
	sort.Slice(out.Sessions, func(i, j int) bool {
		return out.Sessions[i].SessionID < out.Sessions[j].SessionID
	})
	return out
}

func addLifecycleLeaseCount(counts map[string]LifecycleRetentionReason, kind, ownerID string) {
	key := kind + "\x00" + ownerID
	count := counts[key]
	count.Kind = kind
	count.OwnerID = ownerID
	count.Count++
	counts[key] = count
}

func lifecycleLeaseCountKind(counts map[string]LifecycleRetentionReason, kind string) int {
	total := 0
	for _, count := range counts {
		if count.Kind == kind {
			total += count.Count
		}
	}
	return total
}

func sortedLifecycleLeaseCounts(counts map[string]LifecycleRetentionReason) []LifecycleRetentionReason {
	out := make([]LifecycleRetentionReason, 0, len(counts))
	for _, count := range counts {
		out = append(out, count)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].OwnerID < out[j].OwnerID
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
