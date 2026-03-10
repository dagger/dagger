package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/store"
)

type v2Repl struct {
	ID            string             `json:"id"`
	TraceID       string             `json:"traceID"`
	SessionID     string             `json:"sessionID,omitempty"`
	ClientID      string             `json:"clientID,omitempty"`
	RootClientID  string             `json:"rootClientID,omitempty"`
	Name          string             `json:"name"`
	Command       string             `json:"command,omitempty"`
	Mode          string             `json:"mode,omitempty"`
	Status        string             `json:"status"`
	StatusCode    string             `json:"statusCode,omitempty"`
	StartUnixNano int64              `json:"startUnixNano"`
	EndUnixNano   int64              `json:"endUnixNano"`
	CommandCount  int                `json:"commandCount"`
	FirstCommand  string             `json:"firstCommand,omitempty"`
	LastCommand   string             `json:"lastCommand,omitempty"`
	Commands      []v2ReplCommand    `json:"commands,omitempty"`
	Evidence      []v2EntityEvidence `json:"evidence,omitempty"`
	Relations     []v2EntityRelation `json:"relations,omitempty"`
}

type v2ReplCommand struct {
	SpanID        string   `json:"spanID"`
	Name          string   `json:"name"`
	Command       string   `json:"command"`
	Args          []string `json:"args,omitempty"`
	Status        string   `json:"status"`
	StartUnixNano int64    `json:"startUnixNano"`
	EndUnixNano   int64    `json:"endUnixNano"`
}

type v2ReplGroup struct {
	traceID      string
	sessionID    string
	clientID     string
	rootClientID string
	name         string
	command      string
	mode         string
	commands     []v2ReplCommand
}

func (s *Server) handleV2Repls(w http.ResponseWriter, r *http.Request) {
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

	items := make([]v2Repl, 0)
	for _, traceID := range traceIDs {
		traceMeta, err := s.store.GetTrace(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("get trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		spans, _, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		items = append(items, collectV2Repls(traceMeta.Status, traceID, q, spans, scopeIdx)...)
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

func collectV2Repls(traceStatus, traceID string, q v2Query, spans []store.SpanRecord, scopeIdx *derive.ScopeIndex) []v2Repl {
	if scopeIdx == nil {
		return nil
	}

	ordered := append([]store.SpanRecord(nil), spans...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].StartUnixNano != ordered[j].StartUnixNano {
			return ordered[i].StartUnixNano < ordered[j].StartUnixNano
		}
		return ordered[i].SpanID < ordered[j].SpanID
	})

	spanByID := v2SpanByID(ordered)
	groups := map[string]*v2ReplGroup{}
	keys := make([]string, 0)

	for _, sp := range ordered {
		if !intersectsTime(sp.StartUnixNano, spanLastSeen(sp), q.FromUnixNano, q.ToUnixNano) {
			continue
		}
		env, err := decodeV2SpanEnvelope(sp.DataJSON)
		if err != nil {
			continue
		}
		args := getV2StringList(env.Attributes, "dagger.io/shell.handler.args")
		if len(args) == 0 {
			continue
		}

		sessionID := scopeIdx.SessionIDForSpan(sp.SpanID)
		clientID := scopeIdx.ClientIDForSpan(sp.SpanID)
		rootClientID, rootClient := replRootClient(scopeIdx, sessionID, clientID)
		if sessionID == "" && len(scopeIdx.Sessions) == 1 {
			sessionID = scopeIdx.Sessions[0].ID
		}
		if clientID == "" {
			clientID = rootClientID
		}
		if !matchesV2ReplScope(q, traceID, sessionID, clientID, rootClientID) {
			continue
		}

		key := sessionID
		if key == "" {
			key = "trace:" + traceID
		}
		group, ok := groups[key]
		if !ok {
			label := replEntryLabel(spanByID, sp.SpanID, rootClient.CommandArgs)
			command := label
			if command == "" {
				command = strings.Join(rootClient.CommandArgs, " ")
			}
			if command == "" {
				command = replCommandLabel(args, sp.Name)
			}
			group = &v2ReplGroup{
				traceID:      traceID,
				sessionID:    sessionID,
				clientID:     clientID,
				rootClientID: rootClientID,
				name:         labelOrFallback(label, command, "REPL"),
				command:      command,
				mode:         classifyV2ReplMode(rootClient.CommandArgs, label),
			}
			groups[key] = group
			keys = append(keys, key)
		}

		group.commands = append(group.commands, v2ReplCommand{
			SpanID:        spanKey(traceID, sp.SpanID),
			Name:          sp.Name,
			Command:       replCommandLabel(args, sp.Name),
			Args:          args,
			Status:        deriveV2RegistryActivityStatus(traceStatus, sp.StatusCode),
			StartUnixNano: sp.StartUnixNano,
			EndUnixNano:   spanLastSeen(sp),
		})
	}

	items := make([]v2Repl, 0, len(groups))
	for _, key := range keys {
		group := groups[key]
		if group == nil || len(group.commands) == 0 {
			continue
		}
		sort.Slice(group.commands, func(i, j int) bool {
			if group.commands[i].StartUnixNano != group.commands[j].StartUnixNano {
				return group.commands[i].StartUnixNano < group.commands[j].StartUnixNano
			}
			return group.commands[i].SpanID < group.commands[j].SpanID
		})

		status, statusCode := deriveV2ReplStatus(traceStatus, group.commands)
		first := group.commands[0]
		last := group.commands[len(group.commands)-1]
		items = append(items, v2Repl{
			ID:            "repl:" + group.traceID + "/" + nonEmpty(group.sessionID, group.rootClientID, group.clientID),
			TraceID:       group.traceID,
			SessionID:     group.sessionID,
			ClientID:      group.clientID,
			RootClientID:  group.rootClientID,
			Name:          group.name,
			Command:       group.command,
			Mode:          group.mode,
			Status:        status,
			StatusCode:    statusCode,
			StartUnixNano: first.StartUnixNano,
			EndUnixNano:   last.EndUnixNano,
			CommandCount:  len(group.commands),
			FirstCommand:  first.Command,
			LastCommand:   last.Command,
			Commands:      group.commands,
			Evidence:      buildV2ReplEvidence(group, first, last),
			Relations:     buildV2ReplRelations(group),
		})
	}
	return items
}

func matchesV2ReplScope(q v2Query, traceID, sessionID, clientID, rootClientID string) bool {
	if q.TraceID != "" && q.TraceID != traceID {
		return false
	}
	if q.SessionID != "" && q.SessionID != sessionID {
		return false
	}
	if q.ClientID == "" {
		return true
	}
	return q.ClientID == clientID || q.ClientID == rootClientID
}

func replRootClient(scopeIdx *derive.ScopeIndex, sessionID, clientID string) (string, derive.Client) {
	if scopeIdx == nil {
		return "", derive.Client{}
	}
	if sessionID != "" {
		if session, ok := scopeIdx.SessionByID[sessionID]; ok {
			if session.RootClientID != "" {
				return session.RootClientID, scopeIdx.ClientByID[session.RootClientID]
			}
		}
	}
	if clientID != "" {
		if rootClientID := scopeIdx.RootClientByID[clientID]; rootClientID != "" {
			return rootClientID, scopeIdx.ClientByID[rootClientID]
		}
		return clientID, scopeIdx.ClientByID[clientID]
	}
	if len(scopeIdx.Sessions) == 1 {
		rootClientID := scopeIdx.Sessions[0].RootClientID
		if rootClientID != "" {
			return rootClientID, scopeIdx.ClientByID[rootClientID]
		}
	}
	return "", derive.Client{}
}

func replEntryLabel(spanByID map[string]store.SpanRecord, spanID string, commandArgs []string) string {
	best := ""
	for parentID := spanByID[spanID].ParentSpanID; parentID != ""; {
		parent, ok := spanByID[parentID]
		if !ok {
			break
		}
		name := strings.TrimSpace(parent.Name)
		if name != "" {
			lower := strings.ToLower(name)
			switch {
			case strings.HasPrefix(lower, "dagger "):
				return name
			case best == "" && lower != "shell" && lower != "connect":
				best = name
			}
		}
		parentID = parent.ParentSpanID
	}
	if best != "" {
		return best
	}
	return strings.TrimSpace(strings.Join(commandArgs, " "))
}

func classifyV2ReplMode(commandArgs []string, label string) string {
	if cmd, ok := parseV2DaggerShellCommand(commandArgs); ok {
		return cmd.Mode
	}
	lower := strings.ToLower(strings.TrimSpace(label))
	if strings.Contains(lower, " -c ") || strings.Contains(lower, " --command ") {
		return "inline"
	}
	return ""
}

func replCommandLabel(args []string, fallback string) string {
	if len(args) > 0 {
		return strings.Join(args, " ")
	}
	return strings.TrimSpace(fallback)
}

func deriveV2ReplStatus(traceStatus string, commands []v2ReplCommand) (string, string) {
	for _, command := range commands {
		if command.Status == "failed" {
			return "failed", "STATUS_CODE_ERROR"
		}
	}
	if traceStatus == "ingesting" {
		return "running", ""
	}
	return "ready", ""
}

func buildV2ReplEvidence(group *v2ReplGroup, first, last v2ReplCommand) []v2EntityEvidence {
	evidence := []v2EntityEvidence{
		{
			Kind:       "Shell handler",
			Confidence: "high",
			Source:     "dagger.io/shell.handler.args",
			Note:       fmt.Sprintf("%d submitted commands", len(group.commands)),
		},
	}
	if group.command != "" {
		evidence = append(evidence, v2EntityEvidence{
			Kind:       "Command root",
			Confidence: "medium",
			Source:     group.command,
			Note:       "Ancestor command context supplies the REPL entry label.",
		})
	}
	evidence = append(evidence, v2EntityEvidence{
		Kind:       "Command span range",
		Confidence: "high",
		Source:     first.Command,
		Note:       fmt.Sprintf("History ends at %s.", last.Command),
	})
	return evidence
}

func buildV2ReplRelations(group *v2ReplGroup) []v2EntityRelation {
	relations := make([]v2EntityRelation, 0, 2)
	if group.sessionID != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "owned-by",
			Target:     group.sessionID,
			TargetKind: "session",
			Note:       "REPL history stays inside one derived session.",
		})
	}
	if group.rootClientID != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "owned-by",
			Target:     group.rootClientID,
			TargetKind: "client",
			Note:       "Commands inherit one root execution client.",
		})
	}
	return relations
}

func getV2StringList(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	raw, ok := m[key]
	if !ok {
		return nil
	}
	values, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, text)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func labelOrFallback(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}

func nonEmpty(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}
