package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dagger/dagger/internal/odag/derive"
)

type v2Device struct {
	ID                string           `json:"id"`
	MachineID         string           `json:"machineID"`
	FirstSeenUnixNano int64            `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64            `json:"lastSeenUnixNano"`
	TraceCount        int              `json:"traceCount"`
	SessionCount      int              `json:"sessionCount"`
	ClientCount       int              `json:"clientCount"`
	TraceIDs          []string         `json:"traceIDs"`
	SessionIDs        []string         `json:"sessionIDs"`
	ClientIDs         []string         `json:"clientIDs"`
	Clients           []v2DeviceClient `json:"clients"`
}

type v2DeviceClient struct {
	ID                string   `json:"id"`
	TraceID           string   `json:"traceID"`
	SessionID         string   `json:"sessionID"`
	Name              string   `json:"name"`
	CommandArgs       []string `json:"commandArgs"`
	ClientKind        string   `json:"clientKind"`
	ClientOS          string   `json:"clientOS"`
	ClientArch        string   `json:"clientArch"`
	ClientVersion     string   `json:"clientVersion"`
	FirstSeenUnixNano int64    `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64    `json:"lastSeenUnixNano"`
}

type v2DeviceAggregate struct {
	machineID  string
	firstSeen  int64
	lastSeen   int64
	traceIDs   map[string]struct{}
	sessionIDs map[string]struct{}
	clientIDs  map[string]struct{}
	clients    map[string]v2DeviceClient
}

func (s *Server) handleV2Devices(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q)
	if err != nil {
		http.Error(w, fmt.Sprintf("resolve traces: %v", err), http.StatusInternalServerError)
		return
	}

	items := make([]v2Device, 0)
	aggregates := map[string]*v2DeviceAggregate{}
	for _, traceID := range traceIDs {
		_, _, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		scopeQuery := q
		if q.ClientID != "" {
			if rootClientID := strings.TrimSpace(scopeIdx.RootClientByID[q.ClientID]); rootClientID != "" {
				scopeQuery.ClientID = rootClientID
			}
		}
		for _, client := range scopeIdx.Clients {
			if client.ParentClientID != "" {
				continue
			}
			machineID := strings.TrimSpace(client.ClientMachineID)
			if machineID == "" {
				continue
			}
			if !intersectsTime(client.FirstSeenUnixNano, client.LastSeenUnixNano, q.FromUnixNano, q.ToUnixNano) {
				continue
			}
			if !matchesV2Scope(scopeQuery, traceID, client.SessionID, client.ID) {
				continue
			}

			agg := aggregates[machineID]
			if agg == nil {
				agg = &v2DeviceAggregate{
					machineID:  machineID,
					traceIDs:   map[string]struct{}{},
					sessionIDs: map[string]struct{}{},
					clientIDs:  map[string]struct{}{},
					clients:    map[string]v2DeviceClient{},
				}
				aggregates[machineID] = agg
			}
			agg.addClient(client)
		}
	}

	for _, agg := range aggregates {
		items = append(items, agg.item())
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].LastSeenUnixNano != items[j].LastSeenUnixNano {
			return items[i].LastSeenUnixNano > items[j].LastSeenUnixNano
		}
		return items[i].MachineID < items[j].MachineID
	})

	page, next := paginate(items, q.Offset, q.Limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"derivationVersion": derivationVersionV2,
		"items":             page,
		"nextCursor":        next,
	})
}

func (agg *v2DeviceAggregate) addClient(client derive.Client) {
	if agg == nil {
		return
	}
	if client.FirstSeenUnixNano > 0 && (agg.firstSeen == 0 || client.FirstSeenUnixNano < agg.firstSeen) {
		agg.firstSeen = client.FirstSeenUnixNano
	}
	if client.LastSeenUnixNano > agg.lastSeen {
		agg.lastSeen = client.LastSeenUnixNano
	}
	if client.TraceID != "" {
		agg.traceIDs[client.TraceID] = struct{}{}
	}
	if client.SessionID != "" {
		agg.sessionIDs[client.SessionID] = struct{}{}
	}
	if client.ID != "" {
		agg.clientIDs[client.ID] = struct{}{}
	}
	agg.clients[client.ID] = v2DeviceClient{
		ID:                client.ID,
		TraceID:           client.TraceID,
		SessionID:         client.SessionID,
		Name:              client.Name,
		CommandArgs:       append([]string(nil), client.CommandArgs...),
		ClientKind:        client.ClientKind,
		ClientOS:          client.ClientOS,
		ClientArch:        client.ClientArch,
		ClientVersion:     client.ClientVersion,
		FirstSeenUnixNano: client.FirstSeenUnixNano,
		LastSeenUnixNano:  client.LastSeenUnixNano,
	}
}

func (agg *v2DeviceAggregate) item() v2Device {
	if agg == nil {
		return v2Device{
			TraceIDs:   []string{},
			SessionIDs: []string{},
			ClientIDs:  []string{},
			Clients:    []v2DeviceClient{},
		}
	}

	traceIDs := sortedV2DeviceKeys(agg.traceIDs)
	sessionIDs := sortedV2DeviceKeys(agg.sessionIDs)
	clientIDs := sortedV2DeviceKeys(agg.clientIDs)
	clients := make([]v2DeviceClient, 0, len(agg.clients))
	for _, client := range agg.clients {
		clients = append(clients, client)
	}
	sort.Slice(clients, func(i, j int) bool {
		if clients[i].LastSeenUnixNano != clients[j].LastSeenUnixNano {
			return clients[i].LastSeenUnixNano > clients[j].LastSeenUnixNano
		}
		if clients[i].FirstSeenUnixNano != clients[j].FirstSeenUnixNano {
			return clients[i].FirstSeenUnixNano < clients[j].FirstSeenUnixNano
		}
		return clients[i].ID < clients[j].ID
	})

	return v2Device{
		ID:                "device:" + agg.machineID,
		MachineID:         agg.machineID,
		FirstSeenUnixNano: agg.firstSeen,
		LastSeenUnixNano:  agg.lastSeen,
		TraceCount:        len(traceIDs),
		SessionCount:      len(sessionIDs),
		ClientCount:       len(clients),
		TraceIDs:          traceIDs,
		SessionIDs:        sessionIDs,
		ClientIDs:         clientIDs,
		Clients:           clients,
	}
}

func sortedV2DeviceKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return []string{}
	}
	items := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		items = append(items, value)
	}
	sort.Strings(items)
	return items
}
