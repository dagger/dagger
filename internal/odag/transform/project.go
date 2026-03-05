package transform

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/internal/odag/store"
	"google.golang.org/protobuf/proto"
)

type ProjectOptions struct {
	IncludeInternal bool
	ApplyKeepRules  bool
}

var defaultProjectOptions = ProjectOptions{
	IncludeInternal: false,
	ApplyKeepRules:  true,
}

func ProjectTrace(traceID string, spans []store.SpanRecord) (*TraceProjection, error) {
	return ProjectTraceWithOptions(traceID, spans, defaultProjectOptions)
}

func ProjectTraceWithOptions(traceID string, spans []store.SpanRecord, opts ProjectOptions) (*TraceProjection, error) {
	parsedSpans, warnings := parseSpans(spans)
	if len(parsedSpans) == 0 {
		return &TraceProjection{
			TraceID: traceID,
		}, nil
	}

	parsedBySpanID := make(map[string]*parsedSpan, len(parsedSpans))
	for i := range parsedSpans {
		parsedBySpanID[parsedSpans[i].span.SpanID] = &parsedSpans[i]
	}
	markTopLevel(parsedSpans, parsedBySpanID)

	slices.SortFunc(parsedSpans, func(a, b parsedSpan) int {
		if cmp := compareInt64(a.span.EndUnixNano, b.span.EndUnixNano); cmp != 0 {
			return cmp
		}
		if cmp := compareInt64(a.span.StartUnixNano, b.span.StartUnixNano); cmp != 0 {
			return cmp
		}
		return compareString(a.span.SpanID, b.span.SpanID)
	})

	stateToObject := make(map[string]string, len(parsedSpans))
	objectsByID := make(map[string]*ObjectNode, len(parsedSpans))
	nextObjectNum := 1
	typeCounters := map[string]int{}
	events := make([]MutationEvent, 0, len(parsedSpans))
	startUnixNano, endUnixNano := parsedSpans[0].span.StartUnixNano, parsedSpans[0].span.EndUnixNano

	for _, sp := range parsedSpans {
		if startUnixNano == 0 || (sp.span.StartUnixNano > 0 && sp.span.StartUnixNano < startUnixNano) {
			startUnixNano = sp.span.StartUnixNano
		}
		if sp.span.EndUnixNano > endUnixNano {
			endUnixNano = sp.span.EndUnixNano
		}

		event := MutationEvent{
			Index:                 len(events),
			TraceID:               sp.span.TraceID,
			SpanID:                sp.span.SpanID,
			ParentSpanID:          sp.span.ParentSpanID,
			StartUnixNano:         sp.span.StartUnixNano,
			EndUnixNano:           sp.span.EndUnixNano,
			StatusCode:            sp.span.StatusCode,
			StatusMessage:         sp.span.StatusMessage,
			Name:                  sp.span.Name,
			CallDigest:            sp.callDigest,
			ReceiverStateDigest:   sp.receiverStateDigest,
			OutputStateDigest:     sp.outputStateDigest,
			ReturnType:            sp.returnType,
			TopLevel:              sp.isTopLevel,
			CallDepth:             sp.callDepth,
			ParentCallSpanID:      sp.parentCallSpanID,
			ParentCallName:        sp.parentCallName,
			ParentChainIncomplete: sp.parentChainIncomplete,
			Internal:              sp.isInternal,
			Inputs:                sp.inputs,
			Kind:                  "span",
			RawKind:               "span",
		}
		if !sp.isDAGCall {
			events = append(events, event)
			continue
		}
		event.Kind = "call"
		event.RawKind = "call"

		if sp.outputStateDigest == "" || !sp.isObjectOutput {
			events = append(events, event)
			continue
		}

		objectID := ""
		if receiverObjectID, ok := stateToObject[sp.receiverStateDigest]; ok &&
			receiverObjectID != "" &&
			objectsByID[receiverObjectID] != nil &&
			canMutateReceiver(objectsByID[receiverObjectID].TypeName, sp.returnType) {
			objectID = receiverObjectID
			event.Kind = "mutate"
			event.Operation = "mutate"
		}
		if objectID == "" {
			if existingObjectID, ok := stateToObject[sp.outputStateDigest]; ok && existingObjectID != "" {
				objectID = existingObjectID
			}
		}
		if objectID == "" {
			typ := sp.returnType
			if typ == "" {
				typ = "Object"
			}
			typeCounters[typ]++
			objectID = "obj-" + strconv.Itoa(nextObjectNum)
			nextObjectNum++
			objectsByID[objectID] = &ObjectNode{
				ID:       objectID,
				TypeName: typ,
				Alias:    shortTypeName(typ) + "#" + strconv.Itoa(typeCounters[typ]),
			}
			event.Kind = "create"
			event.Operation = "create"
		}
		if event.Operation == "" && event.Kind == "mutate" {
			event.Operation = "mutate"
		}

		obj := objectsByID[objectID]
		stateToObject[sp.outputStateDigest] = objectID
		event.ObjectID = objectID
		event.MissingOutputState = sp.outputStateDigest != "" && sp.outputStatePayload == nil

		state := ObjectState{
			StateDigest:     sp.outputStateDigest,
			CallDigest:      sp.callDigest,
			SpanID:          sp.span.SpanID,
			StartUnixNano:   sp.span.StartUnixNano,
			EndUnixNano:     sp.span.EndUnixNano,
			StatusCode:      sp.span.StatusCode,
			ReceiverState:   sp.receiverStateDigest,
			OutputStateJSON: sp.outputStatePayload,
		}
		obj.StateHistory = append(obj.StateHistory, state)
		if obj.FirstSeenUnixNano == 0 || (sp.span.StartUnixNano > 0 && sp.span.StartUnixNano < obj.FirstSeenUnixNano) {
			obj.FirstSeenUnixNano = sp.span.StartUnixNano
		}
		if sp.span.EndUnixNano > obj.LastSeenUnixNano {
			obj.LastSeenUnixNano = sp.span.EndUnixNano
		}
		if sp.outputStateDigest != "" && sp.outputStatePayload == nil {
			obj.MissingState = true
		}
		events = append(events, event)
	}

	objects := make([]ObjectNode, 0, len(objectsByID))
	topReferencedObjectIDs := make(map[string]struct{}, len(objectsByID))
	keptObjectIDs := make(map[string]struct{}, len(objectsByID))
	hasNonTopObjectActivity := make(map[string]bool, len(objectsByID))

	if opts.ApplyKeepRules {
		for _, event := range events {
			if event.ObjectID == "" || (!opts.IncludeInternal && event.Internal) || event.TopLevel {
				continue
			}
			hasNonTopObjectActivity[event.ObjectID] = true
		}
		markByState := func(stateDigest string, keep bool) {
			if stateDigest == "" {
				return
			}
			objectID, ok := stateToObject[stateDigest]
			if !ok || objectID == "" {
				return
			}
			topReferencedObjectIDs[objectID] = struct{}{}
			if keep {
				keptObjectIDs[objectID] = struct{}{}
			}
		}
		for _, event := range events {
			if !opts.IncludeInternal && event.Internal {
				continue
			}

			keepTopRefs := event.TopLevel && (event.Kind == "create" || event.Kind == "mutate")
			if event.TopLevel {
				if event.ObjectID != "" {
					topReferencedObjectIDs[event.ObjectID] = struct{}{}
				}
				markByState(event.ReceiverStateDigest, keepTopRefs)
				for _, in := range event.Inputs {
					markByState(in.StateDigest, keepTopRefs)
				}
			}

			if event.ObjectID == "" {
				continue
			}
			if event.TopLevel {
				switch event.Kind {
				case "create", "mutate":
					keptObjectIDs[event.ObjectID] = struct{}{}
				case "call":
					// Keep root-level Query object calls by default; other top-level
					// call-only objects are often fan-out noise from scalar selection.
					if strings.HasPrefix(event.Name, "Query.") || hasNonTopObjectActivity[event.ObjectID] {
						keptObjectIDs[event.ObjectID] = struct{}{}
					}
				}
				continue
			}
		}

		for objectID, obj := range objectsByID {
			if _, ok := topReferencedObjectIDs[objectID]; ok {
				obj.ReferencedByTop = true
			}
		}

		// If the default keep rules prune everything (e.g. partial traces), recover
		// with top-level references first, then all objects.
		if len(keptObjectIDs) == 0 {
			if len(topReferencedObjectIDs) > 0 {
				warnings = append(warnings, "default object keep rules yielded no objects; falling back to top-level references")
				for objectID := range topReferencedObjectIDs {
					keptObjectIDs[objectID] = struct{}{}
				}
			} else {
				warnings = append(warnings, "no top-level object references found; falling back to all objects")
				for objectID := range objectsByID {
					keptObjectIDs[objectID] = struct{}{}
				}
			}
		}
	} else {
		for objectID := range objectsByID {
			keptObjectIDs[objectID] = struct{}{}
		}
	}

	for objectID, obj := range objectsByID {
		if _, ok := keptObjectIDs[objectID]; !ok {
			continue
		}
		objects = append(objects, *obj)
	}
	slices.SortFunc(objects, func(a, b ObjectNode) int {
		if cmp := compareInt64(a.FirstSeenUnixNano, b.FirstSeenUnixNano); cmp != 0 {
			return cmp
		}
		return compareString(a.ID, b.ID)
	})

	filteredEvents := make([]MutationEvent, 0, len(events))
	for _, event := range events {
		if !opts.IncludeInternal && event.Internal {
			continue
		}
		if event.ObjectID != "" {
			_, event.Visible = keptObjectIDs[event.ObjectID]
			if !opts.ApplyKeepRules {
				event.Visible = true
			}
		}
		filteredEvents = append(filteredEvents, event)
	}

	return &TraceProjection{
		TraceID:       traceID,
		StartUnixNano: startUnixNano,
		EndUnixNano:   endUnixNano,
		Summary:       summarizeTrace(parsedSpans),
		Objects:       objects,
		Edges:         []ObjectEdge{},
		Events:        filteredEvents,
		Warnings:      warnings,
	}, nil
}

func SnapshotAt(proj *TraceProjection, unixNano int64) Snapshot {
	if proj == nil {
		return Snapshot{}
	}

	if unixNano <= 0 {
		unixNano = proj.EndUnixNano
	}

	snap := Snapshot{
		TraceID:  proj.TraceID,
		UnixNano: unixNano,
		Edges:    proj.Edges,
	}

	activeEventIDs := make([]string, 0)
	snap.Events = make([]MutationEvent, 0, len(proj.Events))
	for _, event := range proj.Events {
		if event.EndUnixNano > 0 && event.EndUnixNano <= unixNano {
			snap.Events = append(snap.Events, event)
		}
		if event.StartUnixNano <= unixNano && (event.EndUnixNano == 0 || event.EndUnixNano > unixNano) {
			activeEventIDs = append(activeEventIDs, event.SpanID)
		}
	}
	snap.ActiveEventIDs = activeEventIDs

	snap.Objects = make([]ObjectNode, 0, len(proj.Objects))
	for _, obj := range proj.Objects {
		stateHistory := make([]ObjectState, 0, len(obj.StateHistory))
		lastSeen := int64(0)
		for _, state := range obj.StateHistory {
			end := state.EndUnixNano
			if end == 0 {
				end = state.StartUnixNano
			}
			if end <= unixNano {
				stateHistory = append(stateHistory, state)
				if end > lastSeen {
					lastSeen = end
				}
			}
		}
		if len(stateHistory) == 0 {
			continue
		}

		cloned := obj
		cloned.StateHistory = stateHistory
		cloned.LastSeenUnixNano = lastSeen
		snap.Objects = append(snap.Objects, cloned)
	}

	return snap
}

func SnapshotAtStep(proj *TraceProjection, step int) Snapshot {
	if proj == nil {
		return Snapshot{}
	}

	stepEventIndexes := make([]int, 0, len(proj.Events))
	eventIndexBySpanID := make(map[string]int, len(proj.Events))
	for idx, event := range proj.Events {
		eventIndexBySpanID[event.SpanID] = idx
		if event.ObjectID != "" && event.Visible {
			stepEventIndexes = append(stepEventIndexes, idx)
		}
	}

	if len(stepEventIndexes) == 0 {
		return SnapshotAt(proj, proj.EndUnixNano)
	}

	if step < 0 {
		step = 0
	}
	if step >= len(stepEventIndexes) {
		step = len(stepEventIndexes) - 1
	}

	boundaryIdx := stepEventIndexes[step]
	boundaryEvent := proj.Events[boundaryIdx]
	boundaryTime := boundaryEvent.EndUnixNano
	if boundaryTime == 0 {
		boundaryTime = boundaryEvent.StartUnixNano
	}

	snap := Snapshot{
		TraceID:  proj.TraceID,
		UnixNano: boundaryTime,
		Edges:    proj.Edges,
	}

	snap.Events = make([]MutationEvent, 0, boundaryIdx+1)
	for idx := 0; idx <= boundaryIdx; idx++ {
		snap.Events = append(snap.Events, proj.Events[idx])
	}

	activeEventIDs := make([]string, 0)
	for _, event := range proj.Events {
		if event.StartUnixNano <= boundaryTime && (event.EndUnixNano == 0 || event.EndUnixNano > boundaryTime) {
			activeEventIDs = append(activeEventIDs, event.SpanID)
		}
	}
	snap.ActiveEventIDs = activeEventIDs

	snap.Objects = make([]ObjectNode, 0, len(proj.Objects))
	for _, obj := range proj.Objects {
		stateHistory := make([]ObjectState, 0, len(obj.StateHistory))
		lastSeen := int64(0)
		for _, state := range obj.StateHistory {
			idx, ok := eventIndexBySpanID[state.SpanID]
			if !ok || idx > boundaryIdx {
				continue
			}
			stateHistory = append(stateHistory, state)
			end := state.EndUnixNano
			if end == 0 {
				end = state.StartUnixNano
			}
			if end > lastSeen {
				lastSeen = end
			}
		}
		if len(stateHistory) == 0 {
			continue
		}

		cloned := obj
		cloned.StateHistory = stateHistory
		cloned.LastSeenUnixNano = lastSeen
		snap.Objects = append(snap.Objects, cloned)
	}

	return snap
}

type dataEnvelope struct {
	Resource   map[string]any `json:"resource"`
	Scope      map[string]any `json:"scope"`
	Attributes map[string]any `json:"attributes"`
}

type parsedSpan struct {
	span                  store.SpanRecord
	resource              map[string]any
	scope                 map[string]any
	attrs                 map[string]any
	isDAGCall             bool
	isTopLevel            bool
	callDepth             int
	parentCallSpanID      string
	parentCallName        string
	parentChainIncomplete bool
	isInternal            bool
	isObjectOutput        bool
	callDigest            string
	receiverStateDigest   string
	outputStateDigest     string
	outputStatePayload    map[string]any
	returnType            string
	inputs                []InputRef
}

func parseSpans(spans []store.SpanRecord) ([]parsedSpan, []string) {
	out := make([]parsedSpan, 0, len(spans))
	warnings := []string{}

	for _, span := range spans {
		env, err := decodeSpanEnvelope(span.DataJSON)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("span %s: decode data_json: %v", span.SpanID, err))
			env = dataEnvelope{}
		}

		p := parsedSpan{
			span:     span,
			resource: env.Resource,
			scope:    env.Scope,
			attrs:    env.Attributes,
		}
		callPayload, _ := getString(p.attrs, telemetry.DagCallAttr)
		callDigest, _ := getString(p.attrs, telemetry.DagDigestAttr)
		outputDigest, _ := getString(p.attrs, telemetry.DagOutputAttr)
		p.callDigest = callDigest
		p.outputStateDigest = outputDigest
		p.isInternal, _ = getBool(p.attrs, telemetry.UIInternalAttr)

		if outputStatePayload, err := decodeOutputStatePayload(p.attrs); err != nil {
			warnings = append(warnings, fmt.Sprintf("span %s: decode output state payload: %v", span.SpanID, err))
		} else {
			p.outputStatePayload = outputStatePayload
		}

		if callPayload != "" {
			p.isDAGCall = true
			callMsg, err := decodeCallPayload(callPayload)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("span %s: decode dag.call payload: %v", span.SpanID, err))
			} else {
				p.receiverStateDigest = callMsg.GetReceiverDigest()
				p.returnType = flattenTypeName(callMsg.GetType())
				p.inputs = collectArgRefs(callMsg.GetArgs())
				if p.callDigest == "" {
					p.callDigest = callMsg.GetDigest()
				}
			}
		}
		if p.outputStateDigest != "" {
			p.isObjectOutput = p.returnType == "" || !isScalarTypeName(p.returnType)
		}
		out = append(out, p)
	}

	// Emitters may omit output-state payloads for repeated immutable IDs.
	// Rehydrate from any previous span in the same trace that carried the state.
	outputStateByDigest := make(map[string]map[string]any, len(out))
	for _, p := range out {
		if p.outputStateDigest == "" || p.outputStatePayload == nil {
			continue
		}
		if _, ok := outputStateByDigest[p.outputStateDigest]; !ok {
			outputStateByDigest[p.outputStateDigest] = p.outputStatePayload
		}
	}
	for i := range out {
		if out[i].outputStatePayload != nil || out[i].outputStateDigest == "" {
			continue
		}
		if cached, ok := outputStateByDigest[out[i].outputStateDigest]; ok {
			out[i].outputStatePayload = cached
		}
	}

	return out, warnings
}

func markTopLevel(spans []parsedSpan, bySpanID map[string]*parsedSpan) {
	for i := range spans {
		sp := &spans[i]
		if !sp.isDAGCall {
			continue
		}

		dagDepth := 0
		parentID := sp.span.ParentSpanID
		for parentID != "" {
			parent, ok := bySpanID[parentID]
			if !ok {
				sp.parentChainIncomplete = true
				break
			}
			if parent.isDAGCall {
				dagDepth++
				if sp.parentCallSpanID == "" {
					sp.parentCallSpanID = parent.span.SpanID
					sp.parentCallName = parent.span.Name
				}
			}
			parentID = parent.span.ParentSpanID
		}
		sp.callDepth = dagDepth
		sp.isTopLevel = dagDepth == 0
	}
}

func decodeSpanEnvelope(dataJSON string) (dataEnvelope, error) {
	if dataJSON == "" {
		return dataEnvelope{}, nil
	}

	var env dataEnvelope
	if err := json.Unmarshal([]byte(dataJSON), &env); err != nil {
		return dataEnvelope{}, err
	}
	return env, nil
}

func decodeCallPayload(payload string) (*callpbv1.Call, error) {
	callBytes, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	var call callpbv1.Call
	if err := proto.Unmarshal(callBytes, &call); err != nil {
		return nil, fmt.Errorf("protobuf decode: %w", err)
	}
	return &call, nil
}

func decodeOutputStatePayload(attrs map[string]any) (map[string]any, error) {
	payload, ok := getString(attrs, telemetry.DagOutputStateAttr)
	if !ok || payload == "" {
		return nil, nil
	}

	// Current experimental parser expects base64(json). If/when the state payload
	// moves to protobuf, this parser can branch on dagger.io/dag.output.state.version.
	_, _ = getString(attrs, telemetry.DagOutputStateVersionAttr)
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	return out, nil
}

func collectArgRefs(args []*callpbv1.Argument) []InputRef {
	if len(args) == 0 {
		return nil
	}

	out := make([]InputRef, 0)
	for _, arg := range args {
		if arg == nil {
			continue
		}
		name := arg.GetName()
		walkLiteralRefs(arg.GetValue(), name, name, &out)
	}
	return out
}

func walkLiteralRefs(lit *callpbv1.Literal, argName string, path string, refs *[]InputRef) {
	if lit == nil {
		return
	}

	switch v := lit.GetValue().(type) {
	case *callpbv1.Literal_CallDigest:
		*refs = append(*refs, InputRef{
			Name:        argName,
			Path:        path,
			StateDigest: v.CallDigest,
		})
	case *callpbv1.Literal_List:
		for i, item := range v.List.GetValues() {
			itemPath := path + "[" + strconv.Itoa(i) + "]"
			walkLiteralRefs(item, argName, itemPath, refs)
		}
	case *callpbv1.Literal_Object:
		for _, field := range v.Object.GetValues() {
			if field == nil {
				continue
			}
			fieldPath := field.GetName()
			if path != "" {
				fieldPath = path + "." + fieldPath
			}
			walkLiteralRefs(field.GetValue(), argName, fieldPath, refs)
		}
	}
}

func flattenTypeName(t *callpbv1.Type) string {
	cur := t
	for cur != nil {
		if cur.GetNamedType() != "" {
			return cur.GetNamedType()
		}
		cur = cur.GetElem()
	}
	return ""
}

func getString(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func getBool(m map[string]any, key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	v, ok := m[key]
	if !ok {
		return false, false
	}
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		if x == "true" {
			return true, true
		}
		if x == "false" {
			return false, true
		}
	}
	return false, false
}

func shortTypeName(typeName string) string {
	if typeName == "" {
		return "Object"
	}
	lastDot := -1
	for i := len(typeName) - 1; i >= 0; i-- {
		if typeName[i] == '.' {
			lastDot = i
			break
		}
	}
	if lastDot >= 0 && lastDot+1 < len(typeName) {
		return typeName[lastDot+1:]
	}
	return typeName
}

func canMutateReceiver(receiverType, returnType string) bool {
	if receiverType == "" || returnType == "" {
		return true
	}
	return sameTypeName(receiverType, returnType)
}

func sameTypeName(a, b string) bool {
	if a == b {
		return true
	}
	return normalizeTypeName(a) == normalizeTypeName(b)
}

func normalizeTypeName(v string) string {
	short := shortTypeName(v)
	return strings.TrimSuffix(short, "ID")
}

func isScalarTypeName(typeName string) bool {
	switch normalizeTypeName(typeName) {
	case "String", "Int", "Float", "Boolean", "Void", "JSON":
		return true
	default:
		return false
	}
}

func summarizeTrace(spans []parsedSpan) TraceSummary {
	if len(spans) == 0 {
		return TraceSummary{}
	}

	roots := make([]SpanSummary, 0, 4)
	commandsBySpanID := make(map[string]CommandSummary)
	var bestCommand *CommandSummary

	for _, sp := range spans {
		dur := spanDurationMs(sp.span.StartUnixNano, sp.span.EndUnixNano)
		if sp.span.ParentSpanID == "" {
			roots = append(roots, SpanSummary{
				SpanID:        sp.span.SpanID,
				Name:          sp.span.Name,
				StartUnixNano: sp.span.StartUnixNano,
				EndUnixNano:   sp.span.EndUnixNano,
				DurationMs:    dur,
			})
		}

		if sp.span.ParentSpanID != "" {
			continue
		}

		commandArgs := getStringSlice(sp.resource, "process.command_args")
		serviceName, _ := getString(sp.resource, "service.name")
		scopeName, _ := getString(sp.scope, "name")
		if len(commandArgs) == 0 && serviceName != "dagger-cli" && scopeName != "dagger.io/cli" {
			continue
		}

		cmd := CommandSummary{
			SpanSummary: SpanSummary{
				SpanID:        sp.span.SpanID,
				Name:          sp.span.Name,
				ParentSpanID:  sp.span.ParentSpanID,
				StartUnixNano: sp.span.StartUnixNano,
				EndUnixNano:   sp.span.EndUnixNano,
				DurationMs:    dur,
			},
			ServiceName: serviceName,
			ScopeName:   scopeName,
			CommandArgs: commandArgs,
		}
		commandsBySpanID[cmd.SpanID] = cmd
		if bestCommand == nil || cmd.DurationMs > bestCommand.DurationMs {
			c := cmd
			bestCommand = &c
		}
	}

	slices.SortFunc(roots, func(a, b SpanSummary) int {
		if cmp := compareInt64(a.StartUnixNano, b.StartUnixNano); cmp != 0 {
			return cmp
		}
		return compareString(a.SpanID, b.SpanID)
	})
	commands := make([]CommandSummary, 0, len(commandsBySpanID))
	for _, cmd := range commandsBySpanID {
		commands = append(commands, cmd)
	}
	slices.SortFunc(commands, func(a, b CommandSummary) int {
		if cmp := compareInt64(a.StartUnixNano, b.StartUnixNano); cmp != 0 {
			return cmp
		}
		return compareString(a.SpanID, b.SpanID)
	})

	title := ""
	if bestCommand != nil {
		if len(bestCommand.CommandArgs) > 0 {
			title = strings.Join(bestCommand.CommandArgs, " ")
		}
		if strings.TrimSpace(title) == "" {
			title = bestCommand.Name
		}
	}
	if strings.TrimSpace(title) == "" && len(roots) > 0 {
		title = roots[0].Name
	}

	return TraceSummary{
		Title:        strings.TrimSpace(title),
		RootSpans:    roots,
		CommandSpans: commands,
	}
}

func spanDurationMs(start, end int64) int64 {
	if start <= 0 || end <= start {
		return 0
	}
	return (end - start) / int64(time.Millisecond)
}

func compareInt64(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func compareString(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func getStringSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok || s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}
