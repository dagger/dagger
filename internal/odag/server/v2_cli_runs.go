package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
)

type v2EntityEvidence struct {
	Kind       string `json:"kind"`
	Confidence string `json:"confidence,omitempty"`
	Source     string `json:"source,omitempty"`
	Note       string `json:"note,omitempty"`
}

type v2EntityRelation struct {
	Relation   string `json:"relation"`
	Target     string `json:"target"`
	TargetKind string `json:"targetKind,omitempty"`
	Note       string `json:"note,omitempty"`
}

type v2CLIRun struct {
	ID                    string             `json:"id"`
	TraceID               string             `json:"traceID"`
	SessionID             string             `json:"sessionID,omitempty"`
	ClientID              string             `json:"clientID,omitempty"`
	RootClientID          string             `json:"rootClientID,omitempty"`
	SubmittedSpanID       string             `json:"submittedSpanID,omitempty"`
	ClientName            string             `json:"clientName,omitempty"`
	Name                  string             `json:"name"`
	Command               string             `json:"command,omitempty"`
	CommandArgs           []string           `json:"commandArgs,omitempty"`
	ChainLabel            string             `json:"chainLabel,omitempty"`
	ChainTokens           []string           `json:"chainTokens,omitempty"`
	Status                string             `json:"status"`
	StatusCode            string             `json:"statusCode,omitempty"`
	StartUnixNano         int64              `json:"startUnixNano"`
	EndUnixNano           int64              `json:"endUnixNano"`
	CallIDs               []string           `json:"callIDs,omitempty"`
	ChainCallIDs          []string           `json:"chainCallIDs,omitempty"`
	CallCount             int                `json:"callCount"`
	ChainDepth            int                `json:"chainDepth"`
	TerminalCallID        string             `json:"terminalCallID,omitempty"`
	TerminalCallName      string             `json:"terminalCallName,omitempty"`
	TerminalReturnType    string             `json:"terminalReturnType,omitempty"`
	TerminalOutputDagqlID string             `json:"terminalOutputDagqlID,omitempty"`
	TerminalObjectID      string             `json:"terminalObjectID,omitempty"`
	PostProcessKinds      []string           `json:"postProcessKinds,omitempty"`
	FollowupSpanIDs       []string           `json:"followupSpanIDs,omitempty"`
	FollowupSpanNames     []string           `json:"followupSpanNames,omitempty"`
	FollowupSpanCount     int                `json:"followupSpanCount"`
	Evidence              []v2EntityEvidence `json:"evidence,omitempty"`
	Relations             []v2EntityRelation `json:"relations,omitempty"`
}

type v2CLIRunCommand struct {
	Command         string
	ChainLabel      string
	ChainTokens     []string
	OutputRequested bool
	AutoApply       bool
}

func (s *Server) handleV2CLIRuns(w http.ResponseWriter, r *http.Request) {
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

	items := make([]v2CLIRun, 0)
	for _, traceID := range traceIDs {
		traceMeta, err := s.store.GetTrace(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("get trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		spans, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		items = append(items, collectV2CLIRuns(traceMeta.Status, traceID, q, spans, proj, scopeIdx)...)
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

func collectV2CLIRuns(
	traceStatus, traceID string,
	q v2Query,
	spans []store.SpanRecord,
	proj *transform.TraceProjection,
	scopeIdx *derive.ScopeIndex,
) []v2CLIRun {
	if proj == nil || scopeIdx == nil {
		return nil
	}

	callsByClient := map[string][]transform.MutationEvent{}
	callsBySession := map[string][]transform.MutationEvent{}
	spansByClient := map[string][]transform.MutationEvent{}
	spansBySession := map[string][]transform.MutationEvent{}
	for _, event := range proj.Events {
		clientID := scopeIdx.ClientIDForSpan(event.SpanID)
		sessionID := scopeIdx.SessionIDForSpan(event.SpanID)
		if !matchesV2Scope(q, traceID, sessionID, clientID) {
			continue
		}
		if !q.IncludeInternal && event.Internal {
			continue
		}
		switch event.RawKind {
		case "call":
			callsByClient[clientID] = append(callsByClient[clientID], event)
			callsBySession[sessionID] = append(callsBySession[sessionID], event)
		default:
			spansByClient[clientID] = append(spansByClient[clientID], event)
			spansBySession[sessionID] = append(spansBySession[sessionID], event)
		}
	}

	items := make([]v2CLIRun, 0)
	for _, client := range scopeIdx.Clients {
		if !matchesV2Scope(q, traceID, client.SessionID, client.ID) {
			continue
		}
		cmd, ok := parseV2DaggerCallCommand(client.CommandArgs)
		if !ok {
			continue
		}
		callEvents := append([]transform.MutationEvent(nil), callsByClient[client.ID]...)
		if len(callEvents) == 0 {
			continue
		}
		sort.Slice(callEvents, func(i, j int) bool {
			if callEvents[i].StartUnixNano != callEvents[j].StartUnixNano {
				return callEvents[i].StartUnixNano < callEvents[j].StartUnixNano
			}
			if callEvents[i].EndUnixNano != callEvents[j].EndUnixNano {
				return callEvents[i].EndUnixNano < callEvents[j].EndUnixNano
			}
			return callEvents[i].SpanID < callEvents[j].SpanID
		})

		callEvents = pipelineCallEvents(callEvents, spans, scopeIdx, client)
		callEvents = pipelineUserCallEvents(callEvents)
		if len(callEvents) == 0 {
			continue
		}
		chainEvents := callEvents
		terminal := terminalCallEvent(chainEvents)
		if terminal == nil {
			continue
		}

		followups := collectFollowupSpans(spansByClient[client.ID], terminal.EndUnixNano)
		startUnixNano, endUnixNano := callEvents[0].StartUnixNano, terminal.EndUnixNano
		if len(followups) > 0 {
			if followups[0].StartUnixNano > 0 && (startUnixNano == 0 || followups[0].StartUnixNano < startUnixNano) {
				startUnixNano = followups[0].StartUnixNano
			}
			lastFollowup := followups[len(followups)-1]
			if lastFollowup.EndUnixNano > endUnixNano {
				endUnixNano = lastFollowup.EndUnixNano
			}
		}
		if !intersectsTime(startUnixNano, endUnixNano, q.FromUnixNano, q.ToUnixNano) {
			continue
		}

		callIDs := make([]string, 0, len(callEvents))
		for _, event := range callEvents {
			callIDs = append(callIDs, spanKey(traceID, event.SpanID))
		}
		chainCallIDs := make([]string, 0, len(chainEvents))
		for _, event := range chainEvents {
			chainCallIDs = append(chainCallIDs, spanKey(traceID, event.SpanID))
		}

		followupSpanIDs := make([]string, 0, len(followups))
		followupSpanNames := make([]string, 0, len(followups))
		for _, event := range followups {
			followupSpanIDs = append(followupSpanIDs, spanKey(traceID, event.SpanID))
			followupSpanNames = append(followupSpanNames, event.Name)
		}

		postProcessKinds := classifyV2CLIRunPostProcess(cmd, *terminal, followups)
		status := deriveV2CLIRunStatus(traceStatus, terminal.StatusCode)
		runID := pipelineRunID(traceID, terminal.SpanID)
		evidence := buildV2CLIRunEvidence(cmd, callEvents, chainEvents, *terminal, followups)
		relations := buildV2CLIRunRelations(client, *terminal, postProcessKinds)
		name := cmd.ChainLabel
		if name == "" {
			name = cmd.Command
		}

		items = append(items, v2CLIRun{
			ID:                    runID,
			TraceID:               traceID,
			SessionID:             client.SessionID,
			ClientID:              client.ID,
			RootClientID:          client.RootClientID,
			SubmittedSpanID:       spanKey(traceID, client.SpanID),
			ClientName:            client.Name,
			Name:                  name,
			Command:               cmd.Command,
			CommandArgs:           client.CommandArgs,
			ChainLabel:            cmd.ChainLabel,
			ChainTokens:           cmd.ChainTokens,
			Status:                status,
			StatusCode:            terminal.StatusCode,
			StartUnixNano:         startUnixNano,
			EndUnixNano:           endUnixNano,
			CallIDs:               callIDs,
			ChainCallIDs:          chainCallIDs,
			CallCount:             len(callEvents),
			ChainDepth:            len(chainEvents),
			TerminalCallID:        spanKey(traceID, terminal.SpanID),
			TerminalCallName:      terminal.Name,
			TerminalReturnType:    terminal.ReturnType,
			TerminalOutputDagqlID: terminal.OutputStateDigest,
			TerminalObjectID:      terminal.ObjectID,
			PostProcessKinds:      postProcessKinds,
			FollowupSpanIDs:       followupSpanIDs,
			FollowupSpanNames:     followupSpanNames,
			FollowupSpanCount:     len(followups),
			Evidence:              evidence,
			Relations:             relations,
		})
	}

	items = append(items, collectV2ShellPipelines(traceStatus, traceID, q, spans, scopeIdx, callsBySession, spansBySession)...)

	return items
}

func pipelineRunID(traceID, terminalSpanID string) string {
	return "pipeline:" + traceID + "/" + terminalSpanID
}

func pipelineCallEvents(
	callEvents []transform.MutationEvent,
	spans []store.SpanRecord,
	scopeIdx *derive.ScopeIndex,
	client derive.Client,
) []transform.MutationEvent {
	var cutoffUnixNano int64
	if len(callEvents) > 0 {
		cutoffUnixNano = callEvents[len(callEvents)-1].StartUnixNano
	}
	boundary := pipelineCommandParseBoundary(spans, scopeIdx, client, cutoffUnixNano)
	if boundary == nil {
		return callEvents
	}
	filtered := make([]transform.MutationEvent, 0, len(callEvents))
	for _, event := range callEvents {
		if event.StartUnixNano < boundary.EndUnixNano {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered
}

func pipelineUserCallEvents(callEvents []transform.MutationEvent) []transform.MutationEvent {
	filtered := make([]transform.MutationEvent, 0, len(callEvents))
	for _, event := range callEvents {
		if isModulePreludeCall(event.Name) {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered
}

type v2ShellPipelineBatch struct {
	traceID       string
	rootSpanID    string
	sessionID     string
	rootClientID  string
	command       string
	startUnixNano int64
	endUnixNano   int64
}

func collectV2ShellPipelines(
	traceStatus, traceID string,
	q v2Query,
	spans []store.SpanRecord,
	scopeIdx *derive.ScopeIndex,
	callsBySession map[string][]transform.MutationEvent,
	spansBySession map[string][]transform.MutationEvent,
) []v2CLIRun {
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
	batches := collectV2ShellPipelineBatches(traceID, q, ordered, scopeIdx, spanByID)
	if len(batches) == 0 {
		return nil
	}

	items := make([]v2CLIRun, 0, len(batches))
	for _, batch := range batches {
		callEvents := shellPipelineCallEvents(batch, callsBySession[batch.sessionID], spanByID, scopeIdx)
		callEvents = pipelineUserCallEvents(callEvents)
		if len(callEvents) == 0 {
			continue
		}
		sort.Slice(callEvents, func(i, j int) bool {
			if callEvents[i].StartUnixNano != callEvents[j].StartUnixNano {
				return callEvents[i].StartUnixNano < callEvents[j].StartUnixNano
			}
			if callEvents[i].EndUnixNano != callEvents[j].EndUnixNano {
				return callEvents[i].EndUnixNano < callEvents[j].EndUnixNano
			}
			return callEvents[i].SpanID < callEvents[j].SpanID
		})

		terminal := terminalCallEvent(callEvents)
		if terminal == nil {
			continue
		}

		followups := shellPipelineFollowupSpans(batch, spansBySession[batch.sessionID], spanByID, scopeIdx, terminal.EndUnixNano)
		startUnixNano := batch.startUnixNano
		endUnixNano := batch.endUnixNano
		if startUnixNano == 0 || (callEvents[0].StartUnixNano > 0 && callEvents[0].StartUnixNano < startUnixNano) {
			startUnixNano = callEvents[0].StartUnixNano
		}
		if endUnixNano == 0 || terminal.EndUnixNano > endUnixNano {
			endUnixNano = terminal.EndUnixNano
		}
		if len(followups) > 0 {
			lastFollowup := followups[len(followups)-1]
			if lastFollowup.EndUnixNano > endUnixNano {
				endUnixNano = lastFollowup.EndUnixNano
			}
		}
		if !intersectsTime(startUnixNano, endUnixNano, q.FromUnixNano, q.ToUnixNano) {
			continue
		}

		callIDs := make([]string, 0, len(callEvents))
		for _, event := range callEvents {
			callIDs = append(callIDs, spanKey(traceID, event.SpanID))
		}
		followupSpanIDs := make([]string, 0, len(followups))
		followupSpanNames := make([]string, 0, len(followups))
		for _, event := range followups {
			followupSpanIDs = append(followupSpanIDs, spanKey(traceID, event.SpanID))
			followupSpanNames = append(followupSpanNames, event.Name)
		}

		clientID := nonEmpty(scopeIdx.ClientIDForSpan(terminal.SpanID), batch.rootClientID)
		sessionID := nonEmpty(scopeIdx.SessionIDForSpan(terminal.SpanID), batch.sessionID)
		rootClientID, _ := replRootClient(scopeIdx, sessionID, clientID)
		if rootClientID == "" {
			rootClientID = nonEmpty(batch.rootClientID, clientID)
		}
		postProcessKinds := classifyV2CLIRunPostProcess(v2CLIRunCommand{Command: batch.command, ChainLabel: batch.command}, *terminal, followups)
		status := deriveV2CLIRunStatus(traceStatus, terminal.StatusCode)

		items = append(items, v2CLIRun{
			ID:                    pipelineRunID(traceID, terminal.SpanID),
			TraceID:               traceID,
			SessionID:             sessionID,
			ClientID:              clientID,
			RootClientID:          rootClientID,
			SubmittedSpanID:       spanKey(traceID, batch.rootSpanID),
			Name:                  batch.command,
			Command:               batch.command,
			ChainLabel:            batch.command,
			Status:                status,
			StatusCode:            terminal.StatusCode,
			StartUnixNano:         startUnixNano,
			EndUnixNano:           endUnixNano,
			CallIDs:               callIDs,
			ChainCallIDs:          append([]string(nil), callIDs...),
			CallCount:             len(callEvents),
			ChainDepth:            len(callEvents),
			TerminalCallID:        spanKey(traceID, terminal.SpanID),
			TerminalCallName:      terminal.Name,
			TerminalReturnType:    terminal.ReturnType,
			TerminalOutputDagqlID: terminal.OutputStateDigest,
			TerminalObjectID:      terminal.ObjectID,
			PostProcessKinds:      postProcessKinds,
			FollowupSpanIDs:       followupSpanIDs,
			FollowupSpanNames:     followupSpanNames,
			FollowupSpanCount:     len(followups),
			Evidence:              buildV2CLIRunEvidence(v2CLIRunCommand{Command: batch.command, ChainLabel: batch.command}, callEvents, callEvents, *terminal, followups),
			Relations:             buildV2CLIRunRelations(derive.Client{ID: clientID, Name: batch.command}, *terminal, postProcessKinds),
		})
	}

	return items
}

func collectV2ShellPipelineBatches(
	traceID string,
	q v2Query,
	spans []store.SpanRecord,
	scopeIdx *derive.ScopeIndex,
	spanByID map[string]store.SpanRecord,
) []v2ShellPipelineBatch {
	batches := make([]v2ShellPipelineBatch, 0)
	seen := map[string]int{}
	for _, sp := range spans {
		if !intersectsTime(sp.StartUnixNano, spanLastSeen(sp), q.FromUnixNano, q.ToUnixNano) {
			continue
		}
		env, err := decodeV2SpanEnvelope(sp.DataJSON)
		if err != nil {
			continue
		}
		if len(getV2StringList(env.Attributes, "dagger.io/shell.handler.args")) == 0 {
			continue
		}
		rootSpanID := shellPipelineRootSpanID(spanByID, sp)
		if rootSpanID == "" {
			continue
		}
		root, ok := spanByID[rootSpanID]
		if !ok {
			continue
		}
		sessionID := scopeIdx.SessionIDForSpan(sp.SpanID)
		clientID := scopeIdx.ClientIDForSpan(sp.SpanID)
		rootClientID, _ := replRootClient(scopeIdx, sessionID, clientID)
		if sessionID == "" && len(scopeIdx.Sessions) == 1 {
			sessionID = scopeIdx.Sessions[0].ID
		}
		if !matchesV2Scope(q, traceID, sessionID, nonEmpty(clientID, rootClientID)) {
			continue
		}
		if idx, ok := seen[rootSpanID]; ok {
			if batches[idx].sessionID == "" {
				batches[idx].sessionID = sessionID
			}
			if batches[idx].rootClientID == "" {
				batches[idx].rootClientID = rootClientID
			}
			continue
		}
		seen[rootSpanID] = len(batches)
		batches = append(batches, v2ShellPipelineBatch{
			traceID:       traceID,
			rootSpanID:    rootSpanID,
			sessionID:     sessionID,
			rootClientID:  rootClientID,
			command:       strings.TrimSpace(root.Name),
			startUnixNano: root.StartUnixNano,
			endUnixNano:   root.EndUnixNano,
		})
	}
	return batches
}

func shellPipelineRootSpanID(spanByID map[string]store.SpanRecord, sp store.SpanRecord) string {
	if sp.ParentSpanID == "" {
		return ""
	}
	return sp.ParentSpanID
}

func shellPipelineCallEvents(
	batch v2ShellPipelineBatch,
	callEvents []transform.MutationEvent,
	spanByID map[string]store.SpanRecord,
	scopeIdx *derive.ScopeIndex,
) []transform.MutationEvent {
	filtered := make([]transform.MutationEvent, 0, len(callEvents))
	for _, event := range callEvents {
		if shellPipelineOwnsEvent(batch, event, spanByID, scopeIdx) {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func shellPipelineFollowupSpans(
	batch v2ShellPipelineBatch,
	events []transform.MutationEvent,
	spanByID map[string]store.SpanRecord,
	scopeIdx *derive.ScopeIndex,
	terminalEndUnixNano int64,
) []transform.MutationEvent {
	filtered := make([]transform.MutationEvent, 0, len(events))
	for _, event := range events {
		if event.StartUnixNano < terminalEndUnixNano {
			continue
		}
		if shellPipelineOwnsEvent(batch, event, spanByID, scopeIdx) {
			filtered = append(filtered, event)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].StartUnixNano != filtered[j].StartUnixNano {
			return filtered[i].StartUnixNano < filtered[j].StartUnixNano
		}
		return filtered[i].SpanID < filtered[j].SpanID
	})
	return filtered
}

func shellPipelineOwnsEvent(
	batch v2ShellPipelineBatch,
	event transform.MutationEvent,
	spanByID map[string]store.SpanRecord,
	scopeIdx *derive.ScopeIndex,
) bool {
	if event.StartUnixNano < batch.startUnixNano {
		return false
	}
	if batch.endUnixNano > 0 && event.StartUnixNano > batch.endUnixNano {
		return false
	}
	if terminalDescendsFrom(spanByID, event.SpanID, batch.rootSpanID) || event.ParentSpanID == batch.rootSpanID {
		return true
	}
	if batch.sessionID == "" || scopeIdx == nil {
		return false
	}
	if scopeIdx.SessionIDForSpan(event.SpanID) != batch.sessionID {
		return false
	}
	return event.EndUnixNano == 0 || event.EndUnixNano >= batch.startUnixNano
}

func pipelineCommandParseBoundary(
	spans []store.SpanRecord,
	scopeIdx *derive.ScopeIndex,
	client derive.Client,
	cutoffUnixNano int64,
) *store.SpanRecord {
	var sameClient *store.SpanRecord
	var sameSession *store.SpanRecord
	var traceWide *store.SpanRecord
	var sameClientAny *store.SpanRecord
	var sameSessionAny *store.SpanRecord
	var traceWideAny *store.SpanRecord
	for _, sp := range spans {
		if !isPipelineCommandParseSpan(sp.Name) {
			continue
		}
		if pipelineSpanRecordLater(traceWideAny, sp) {
			candidate := sp
			traceWideAny = &candidate
		}
		switch {
		case client.ID != "" && scopeIdx != nil && scopeIdx.ClientIDForSpan(sp.SpanID) == client.ID:
			if pipelineSpanRecordLater(sameClientAny, sp) {
				candidate := sp
				sameClientAny = &candidate
			}
			if cutoffUnixNano > 0 && sp.EndUnixNano > cutoffUnixNano {
				continue
			}
			if pipelineSpanRecordLater(sameClient, sp) {
				candidate := sp
				sameClient = &candidate
			}
		case client.SessionID != "" && scopeIdx != nil && scopeIdx.SessionIDForSpan(sp.SpanID) == client.SessionID:
			if pipelineSpanRecordLater(sameSessionAny, sp) {
				candidate := sp
				sameSessionAny = &candidate
			}
			if cutoffUnixNano > 0 && sp.EndUnixNano > cutoffUnixNano {
				continue
			}
			if pipelineSpanRecordLater(sameSession, sp) {
				candidate := sp
				sameSession = &candidate
			}
		default:
			if cutoffUnixNano > 0 && sp.EndUnixNano > cutoffUnixNano {
				continue
			}
			if pipelineSpanRecordLater(traceWide, sp) {
				candidate := sp
				traceWide = &candidate
			}
		}
	}
	if sameClient != nil {
		return sameClient
	}
	if sameSession != nil {
		return sameSession
	}
	if sameClientAny != nil {
		return sameClientAny
	}
	if sameSessionAny != nil {
		return sameSessionAny
	}
	if traceWide != nil {
		return traceWide
	}
	return traceWideAny
}

func pipelineSpanRecordLater(best *store.SpanRecord, candidate store.SpanRecord) bool {
	if best == nil {
		return true
	}
	if candidate.EndUnixNano != best.EndUnixNano {
		return candidate.EndUnixNano > best.EndUnixNano
	}
	if candidate.StartUnixNano != best.StartUnixNano {
		return candidate.StartUnixNano > best.StartUnixNano
	}
	return candidate.SpanID > best.SpanID
}

func isPipelineCommandParseSpan(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "parsing command line arguments")
}

func parseV2DaggerCallCommand(args []string) (v2CLIRunCommand, bool) {
	if len(args) == 0 {
		return v2CLIRunCommand{}, false
	}

	command := strings.Join(args, " ")
	keep := []string{}
	fullCall := []string{}
	var seenCommand bool
	var isCall bool
	var isFlag bool
	var keepFlag bool
	var pastConstructor bool
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			isFlag = true
			if strings.Contains(arg, "=") {
				isFlag = false
			}
			if seenCommand {
				if isCall {
					if pastConstructor {
						keep = append(keep, arg)
						keepFlag = true
					}
				}
				continue
			}
			continue
		}
		if isFlag {
			isFlag = false
			if keepFlag {
				keep = append(keep, arg)
				keepFlag = false
			}
			continue
		}
		seenCommand = true
		if isCall {
			keep = append(keep, arg)
			pastConstructor = true
			continue
		}
		if len(keep) == 0 && arg == "call" {
			isCall = true
			if i+1 < len(args) {
				fullCall = append(fullCall, args[i+1:]...)
			}
			continue
		}
	}
	if !isCall {
		return v2CLIRunCommand{}, false
	}
	if len(keep) == 0 {
		keep = fullCall
	}
	return v2CLIRunCommand{
		Command:         command,
		ChainLabel:      strings.Join(keep, " "),
		ChainTokens:     keep,
		OutputRequested: hasV2CommandFlag(args, "-o", "--output"),
		AutoApply:       hasV2CommandFlag(args, "-y", "--auto-apply"),
	}, true
}

func hasV2CommandFlag(args []string, shortFlag, longFlag string) bool {
	for _, arg := range args {
		switch {
		case arg == shortFlag, arg == longFlag:
			return true
		case strings.HasPrefix(arg, shortFlag+"="), strings.HasPrefix(arg, longFlag+"="):
			return true
		}
	}
	return false
}

func terminalCallEvent(events []transform.MutationEvent) *transform.MutationEvent {
	if len(events) == 0 {
		return nil
	}
	best := events[0]
	for _, event := range events[1:] {
		bestEnd := best.EndUnixNano
		if bestEnd <= 0 {
			bestEnd = best.StartUnixNano
		}
		eventEnd := event.EndUnixNano
		if eventEnd <= 0 {
			eventEnd = event.StartUnixNano
		}
		if best.EndUnixNano <= 0 && event.EndUnixNano > 0 {
			continue
		}
		if event.EndUnixNano <= 0 && best.EndUnixNano > 0 {
			best = event
			continue
		}
		if eventEnd > bestEnd ||
			(eventEnd == bestEnd && event.StartUnixNano > best.StartUnixNano) ||
			(eventEnd == bestEnd && event.StartUnixNano == best.StartUnixNano && event.SpanID > best.SpanID) {
			best = event
		}
	}
	return &best
}

func collectFollowupSpans(events []transform.MutationEvent, terminalEndUnixNano int64) []transform.MutationEvent {
	out := make([]transform.MutationEvent, 0, len(events))
	for _, event := range events {
		if event.StartUnixNano < terminalEndUnixNano {
			continue
		}
		out = append(out, event)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartUnixNano != out[j].StartUnixNano {
			return out[i].StartUnixNano < out[j].StartUnixNano
		}
		return out[i].SpanID < out[j].SpanID
	})
	return out
}

func classifyV2CLIRunPostProcess(cmd v2CLIRunCommand, terminal transform.MutationEvent, followups []transform.MutationEvent) []string {
	set := map[string]struct{}{}
	switch terminal.ReturnType {
	case "Changeset":
		set["changeset-preview"] = struct{}{}
		if cmd.AutoApply {
			set["changeset-auto-apply"] = struct{}{}
		}
	case "LLM":
		set["prompt-handoff"] = struct{}{}
	default:
		if cmd.OutputRequested {
			set["output-export"] = struct{}{}
		}
		if terminal.OutputStateDigest != "" {
			set["print-object-id"] = struct{}{}
		} else if terminal.ReturnType != "" {
			set["print-value"] = struct{}{}
		}
	}
	for _, event := range followups {
		name := strings.ToLower(strings.TrimSpace(event.Name))
		switch {
		case strings.Contains(name, "analyzing changes"):
			set["changeset-preview"] = struct{}{}
		case strings.Contains(name, "applying changes"):
			set["changeset-apply"] = struct{}{}
		}
	}
	return setToSortedSlice(set)
}

func deriveV2CLIRunStatus(traceStatus, terminalStatus string) string {
	if terminalStatus != "" && terminalStatus != "STATUS_CODE_OK" && terminalStatus != "OK" {
		return "failed"
	}
	if traceStatus == "ingesting" {
		return "running"
	}
	return "ready"
}

func buildV2CLIRunEvidence(cmd v2CLIRunCommand, allCalls, chainCalls []transform.MutationEvent, terminal transform.MutationEvent, followups []transform.MutationEvent) []v2EntityEvidence {
	evidence := []v2EntityEvidence{
		{
			Kind:       "Command root",
			Confidence: "high",
			Source:     "process.command_args",
			Note:       cmd.ChainLabel,
		},
		{
			Kind:       "Call chain",
			Confidence: "high",
			Source:     "client-owned DAGQL calls",
			Note:       fmt.Sprintf("%d top-level calls, %d total calls", len(chainCalls), len(allCalls)),
		},
		{
			Kind:       "Terminal output",
			Confidence: "high",
			Source:     terminal.Name,
			Note:       terminalSummaryNote(terminal),
		},
	}
	if cmd.OutputRequested {
		evidence = append(evidence, v2EntityEvidence{
			Kind:       "CLI flag",
			Confidence: "high",
			Source:     "--output",
			Note:       "Run requested CLI export handling.",
		})
	}
	if cmd.AutoApply {
		evidence = append(evidence, v2EntityEvidence{
			Kind:       "CLI flag",
			Confidence: "high",
			Source:     "--auto-apply",
			Note:       "Run requested automatic changeset apply.",
		})
	}
	if len(followups) > 0 {
		names := make([]string, 0, min(3, len(followups)))
		for i := 0; i < len(followups) && i < 3; i++ {
			names = append(names, followups[i].Name)
		}
		evidence = append(evidence, v2EntityEvidence{
			Kind:       "Follow-up spans",
			Confidence: "medium",
			Source:     "same client scope",
			Note:       strings.Join(names, ", "),
		})
	}
	return evidence
}

func buildV2CLIRunRelations(client derive.Client, terminal transform.MutationEvent, postProcessKinds []string) []v2EntityRelation {
	relations := []v2EntityRelation{
		{
			Relation:   "owned-by",
			Target:     client.ID,
			TargetKind: "client",
			Note:       client.Name,
		},
	}
	if terminal.OutputStateDigest != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "returns",
			Target:     terminal.OutputStateDigest,
			TargetKind: "dagql-state",
			Note:       terminal.ReturnType,
		})
	}
	if terminal.ObjectID != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "materializes",
			Target:     terminal.ObjectID,
			TargetKind: "object-binding",
			Note:       terminal.ReturnType,
		})
	}
	for _, kind := range postProcessKinds {
		relations = append(relations, v2EntityRelation{
			Relation:   "continues-as",
			Target:     kind,
			TargetKind: "post-process",
		})
	}
	return relations
}

func terminalSummaryNote(event transform.MutationEvent) string {
	parts := []string{}
	if event.ReturnType != "" {
		parts = append(parts, "returns "+event.ReturnType)
	}
	if event.OutputStateDigest != "" {
		parts = append(parts, "dagql="+event.OutputStateDigest)
	}
	if len(parts) == 0 {
		return event.Name
	}
	return strings.Join(parts, ", ")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
