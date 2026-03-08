package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/transform"
)

type v2Shell struct {
	ID               string             `json:"id"`
	TraceID          string             `json:"traceID"`
	SessionID        string             `json:"sessionID,omitempty"`
	ClientID         string             `json:"clientID,omitempty"`
	RootClientID     string             `json:"rootClientID,omitempty"`
	ClientName       string             `json:"clientName,omitempty"`
	Name             string             `json:"name"`
	Command          string             `json:"command,omitempty"`
	CommandArgs      []string           `json:"commandArgs,omitempty"`
	Mode             string             `json:"mode,omitempty"`
	EntryLabel       string             `json:"entryLabel,omitempty"`
	Status           string             `json:"status"`
	StatusCode       string             `json:"statusCode,omitempty"`
	StartUnixNano    int64              `json:"startUnixNano"`
	EndUnixNano      int64              `json:"endUnixNano"`
	ChildClientIDs   []string           `json:"childClientIDs,omitempty"`
	ChildClientCount int                `json:"childClientCount"`
	CallIDs          []string           `json:"callIDs,omitempty"`
	CallCount        int                `json:"callCount"`
	SpanCount        int                `json:"spanCount"`
	ActivityNames    []string           `json:"activityNames,omitempty"`
	Evidence         []v2EntityEvidence `json:"evidence,omitempty"`
	Relations        []v2EntityRelation `json:"relations,omitempty"`
}

type v2ShellCommand struct {
	Command    string
	Mode       string
	EntryLabel string
}

func (s *Server) handleV2Shells(w http.ResponseWriter, r *http.Request) {
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

	items := make([]v2Shell, 0)
	for _, traceID := range traceIDs {
		traceMeta, err := s.store.GetTrace(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("get trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		_, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		items = append(items, collectV2Shells(traceMeta.Status, traceID, q, proj, scopeIdx)...)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].StartUnixNano != items[j].StartUnixNano {
			return items[i].StartUnixNano < items[j].StartUnixNano
		}
		return items[i].ID < items[j].ID
	})

	page, next := paginate(items, q.Offset, q.Limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"derivationVersion": derivationVersionV2,
		"items":             page,
		"nextCursor":        next,
	})
}

func collectV2Shells(traceStatus, traceID string, q v2Query, proj *transform.TraceProjection, scopeIdx *derive.ScopeIndex) []v2Shell {
	if proj == nil || scopeIdx == nil {
		return nil
	}

	eventsByClient := map[string][]transform.MutationEvent{}
	for _, event := range proj.Events {
		clientID := scopeIdx.ClientIDForSpan(event.SpanID)
		if clientID == "" {
			continue
		}
		if !q.IncludeInternal && event.Internal {
			continue
		}
		eventsByClient[clientID] = append(eventsByClient[clientID], event)
	}

	items := make([]v2Shell, 0)
	for _, client := range scopeIdx.Clients {
		if client.ParentClientID != "" || (client.RootClientID != "" && client.RootClientID != client.ID) {
			continue
		}

		cmd, ok := parseV2DaggerShellCommand(client.CommandArgs)
		if !ok {
			continue
		}

		clientTree := shellClientTree(scopeIdx, client.ID)
		if !matchesV2ShellScope(q, traceID, client.SessionID, clientTree) {
			continue
		}

		childClientIDs := make([]string, 0, max(0, len(clientTree)-1))
		ownedEvents := make([]transform.MutationEvent, 0)
		callEvents := make([]transform.MutationEvent, 0)
		activityNames := make([]string, 0, 6)
		activitySeen := map[string]struct{}{}

		startUnixNano, endUnixNano := client.FirstSeenUnixNano, client.LastSeenUnixNano
		for _, treeClient := range clientTree {
			if treeClient.ID != client.ID {
				childClientIDs = append(childClientIDs, treeClient.ID)
			}
			if treeClient.FirstSeenUnixNano > 0 && (startUnixNano == 0 || treeClient.FirstSeenUnixNano < startUnixNano) {
				startUnixNano = treeClient.FirstSeenUnixNano
			}
			if treeClient.LastSeenUnixNano > endUnixNano {
				endUnixNano = treeClient.LastSeenUnixNano
			}
			for _, event := range eventsByClient[treeClient.ID] {
				ownedEvents = append(ownedEvents, event)
				if event.StartUnixNano > 0 && (startUnixNano == 0 || event.StartUnixNano < startUnixNano) {
					startUnixNano = event.StartUnixNano
				}
				if event.EndUnixNano > endUnixNano {
					endUnixNano = event.EndUnixNano
				}
				if event.RawKind != "call" {
					continue
				}
				callEvents = append(callEvents, event)
				if event.Name == "" {
					continue
				}
				if _, ok := activitySeen[event.Name]; ok {
					continue
				}
				activitySeen[event.Name] = struct{}{}
				activityNames = append(activityNames, event.Name)
			}
		}

		if !intersectsTime(startUnixNano, endUnixNano, q.FromUnixNano, q.ToUnixNano) {
			continue
		}

		sort.Slice(childClientIDs, func(i, j int) bool {
			return childClientIDs[i] < childClientIDs[j]
		})
		sort.Slice(ownedEvents, func(i, j int) bool {
			if ownedEvents[i].StartUnixNano != ownedEvents[j].StartUnixNano {
				return ownedEvents[i].StartUnixNano < ownedEvents[j].StartUnixNano
			}
			return ownedEvents[i].SpanID < ownedEvents[j].SpanID
		})
		sort.Slice(callEvents, func(i, j int) bool {
			if callEvents[i].StartUnixNano != callEvents[j].StartUnixNano {
				return callEvents[i].StartUnixNano < callEvents[j].StartUnixNano
			}
			return callEvents[i].SpanID < callEvents[j].SpanID
		})

		callIDs := make([]string, 0, len(callEvents))
		for _, event := range callEvents {
			callIDs = append(callIDs, spanKey(traceID, event.SpanID))
		}

		status, statusCode := deriveV2ShellStatus(traceStatus, ownedEvents)
		name := cmd.EntryLabel
		if name == "" {
			name = cmd.Command
		}
		rootClientID := client.RootClientID
		if rootClientID == "" {
			rootClientID = client.ID
		}

		items = append(items, v2Shell{
			ID:               "shell:" + traceID + "/" + client.ID,
			TraceID:          traceID,
			SessionID:        client.SessionID,
			ClientID:         client.ID,
			RootClientID:     rootClientID,
			ClientName:       client.Name,
			Name:             name,
			Command:          cmd.Command,
			CommandArgs:      client.CommandArgs,
			Mode:             cmd.Mode,
			EntryLabel:       cmd.EntryLabel,
			Status:           status,
			StatusCode:       statusCode,
			StartUnixNano:    startUnixNano,
			EndUnixNano:      endUnixNano,
			ChildClientIDs:   childClientIDs,
			ChildClientCount: len(childClientIDs),
			CallIDs:          callIDs,
			CallCount:        len(callEvents),
			SpanCount:        len(ownedEvents),
			ActivityNames:    activityNames,
			Evidence:         buildV2ShellEvidence(client, cmd, childClientIDs, callEvents, activityNames),
			Relations:        buildV2ShellRelations(client, childClientIDs),
		})
	}

	return items
}

func parseV2DaggerShellCommand(args []string) (v2ShellCommand, bool) {
	if len(args) == 0 {
		return v2ShellCommand{}, false
	}

	command := strings.Join(args, " ")
	var foundShell bool
	inline := ""
	files := []string{}

	for i := 1; i < len(args); i++ {
		arg := args[i]
		if !foundShell {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			if arg != "shell" {
				return v2ShellCommand{}, false
			}
			foundShell = true
			continue
		}

		switch {
		case strings.HasPrefix(arg, "--command="):
			inline = strings.TrimPrefix(arg, "--command=")
		case strings.HasPrefix(arg, "-c="):
			inline = strings.TrimPrefix(arg, "-c=")
		case arg == "--command" || arg == "-c":
			if i+1 < len(args) {
				inline = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "-"):
			continue
		default:
			files = append(files, arg)
		}
	}

	if !foundShell {
		return v2ShellCommand{}, false
	}

	mode := "interactive"
	entryLabel := "interactive shell"
	if inline != "" {
		mode = "inline"
		entryLabel = inline
	} else if len(files) > 0 {
		mode = "file"
		entryLabel = files[0]
		if len(files) > 1 {
			entryLabel = fmt.Sprintf("%s (+%d files)", files[0], len(files)-1)
		}
	}

	return v2ShellCommand{
		Command:    command,
		Mode:       mode,
		EntryLabel: entryLabel,
	}, true
}

func shellClientTree(scopeIdx *derive.ScopeIndex, rootClientID string) []derive.Client {
	if scopeIdx == nil || rootClientID == "" {
		return nil
	}
	out := make([]derive.Client, 0, len(scopeIdx.Clients))
	for _, client := range scopeIdx.Clients {
		switch {
		case client.ID == rootClientID:
			out = append(out, client)
		case client.RootClientID == rootClientID:
			out = append(out, client)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FirstSeenUnixNano != out[j].FirstSeenUnixNano {
			return out[i].FirstSeenUnixNano < out[j].FirstSeenUnixNano
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func matchesV2ShellScope(q v2Query, traceID, sessionID string, clients []derive.Client) bool {
	if q.TraceID != "" && q.TraceID != traceID {
		return false
	}
	if q.SessionID != "" && q.SessionID != sessionID {
		return false
	}
	if q.ClientID == "" {
		return true
	}
	for _, client := range clients {
		if client.ID == q.ClientID {
			return true
		}
	}
	return false
}

func deriveV2ShellStatus(traceStatus string, events []transform.MutationEvent) (string, string) {
	for _, event := range events {
		if event.StatusCode != "" && event.StatusCode != "STATUS_CODE_OK" && event.StatusCode != "OK" {
			return "failed", event.StatusCode
		}
	}
	if traceStatus == "ingesting" {
		return "running", ""
	}
	return "ready", ""
}

func buildV2ShellEvidence(client derive.Client, cmd v2ShellCommand, childClientIDs []string, callEvents []transform.MutationEvent, activityNames []string) []v2EntityEvidence {
	evidence := []v2EntityEvidence{
		{
			Kind:       "Command root",
			Confidence: "high",
			Source:     "process.command_args",
			Note:       cmd.EntryLabel,
		},
		{
			Kind:       "Session scope",
			Confidence: "high",
			Source:     "explicit session and client tree",
			Note:       fmt.Sprintf("%d descendant clients", len(childClientIDs)),
		},
		{
			Kind:       "Owned calls",
			Confidence: "medium",
			Source:     "root client tree",
			Note:       fmt.Sprintf("%d calls: %s", len(callEvents), shellActivityNote(activityNames)),
		},
	}
	if client.ClientKind != "" {
		evidence = append(evidence, v2EntityEvidence{
			Kind:       "Client kind",
			Confidence: "medium",
			Source:     "dagger.io/engine.client.kind",
			Note:       client.ClientKind,
		})
	}
	return evidence
}

func buildV2ShellRelations(client derive.Client, childClientIDs []string) []v2EntityRelation {
	relations := []v2EntityRelation{
		{
			Relation:   "owned-by",
			Target:     client.SessionID,
			TargetKind: "session",
		},
		{
			Relation:   "rooted-at",
			Target:     client.ID,
			TargetKind: "client",
			Note:       client.Name,
		},
	}
	for _, childClientID := range childClientIDs {
		relations = append(relations, v2EntityRelation{
			Relation:   "contains",
			Target:     childClientID,
			TargetKind: "client",
		})
	}
	return relations
}

func shellActivityNote(activityNames []string) string {
	if len(activityNames) == 0 {
		return "no DAGQL calls yet"
	}
	limit := min(3, len(activityNames))
	out := strings.Join(activityNames[:limit], ", ")
	if len(activityNames) > limit {
		return fmt.Sprintf("%s (+%d more)", out, len(activityNames)-limit)
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
