package core

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/dagger/dagger/dagql"
)

func (observation *workspaceInputCall) preparedOrigins() workspaceInputSummary {
	prepared := observation.initial
	prepared.Sources = slices.Clone(prepared.Sources)
	prepared.FindUps = slices.Clone(prepared.FindUps)
	prepared.Issues = slices.Clone(prepared.Issues)
	prepared.reads = nil
	prepared.findUps = nil
	return prepared
}

func (observation *workspaceInputCall) capture(ctx context.Context, value dagql.AnyResult) (workspaceInputFact, error) {
	c, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		return workspaceInputFact{}, err
	}
	fact := workspaceInputFact{
		Operation:            workspaceInputOperation(ctx, observation.request.ResultCall),
		Complete:             true,
		Prepared:             observation.preparedOrigins(),
		ContextInputs:        observation.summary().capturedContextInputs(),
		CapabilitiesComplete: true,
	}
	if runtime, ready := observation.reads.runtimeSnapshot(); ready {
		fact.Prepared.merge(runtime)
	} else if err := observation.currentRuntimeInputs(ctx, &fact.Prepared); err != nil {
		return workspaceInputFact{}, err
	}
	fact.Prepared.reads = nil
	fact.Prepared.findUps = nil
	fact.FindUps = observation.summary().findUps
	for _, source := range observation.summary().reads {
		bindings := c.matchingBindings(source.producer, observation.bindings)
		if len(bindings) == 0 {
			fact.Issues = append(fact.Issues, "recorded source read has no prepared Workspace input binding for "+fact.Operation)
			continue
		}
		for _, binding := range bindings {
			fact.Reads = append(fact.Reads, workspaceBoundRead{Input: binding.path, Source: source})
		}
	}
	for _, source := range observation.summary().Sources {
		fact.Issues = append(fact.Issues, source.Issues...)
	}
	if value != nil {
		if !observation.prepared {
			c.valueVisitor = func(value dagql.AnyResult) error { observation.reads.checkValueOrigin(ctx, value); return nil }
		}
		visitWorkspace := func(output workspaceInputPath, ref *dagql.ResultCallRef, workspace string) {
			bindings := c.matchingBindings(ref, observation.bindings)
			if len(bindings) == 0 && observation.request.Module == nil && observation.request.ReceiverTypeName == "Workspace" && workspaceInputView(observation.request) {
				bindings = c.matchingBindings(observation.request.Receiver, observation.bindings)
			}
			if len(bindings) == 0 {
				// A native constructor can introduce a new workspace. Its
				// capability is fixed on this producer and cannot be rebound.
				if observation.request.Module == nil && !observation.storedField {
					fact.Retained = append(fact.Retained, workspaceRetainedInput{Output: output, Workspace: workspace})
					return
				}
				fact.CapabilitiesComplete = false
				fact.Issues = append(fact.Issues, "retained Workspace has no prepared input binding for "+fact.Operation)
				return
			}
			for _, binding := range bindings {
				fact.Retained = append(fact.Retained, workspaceRetainedInput{Output: output, Input: binding.path, Workspace: binding.workspace})
			}
		}
		_, workspaceOutput := dagql.UnwrapAs[*Workspace](value)
		if workspaceOutput && observation.request.Module == nil && workspaceInputView(observation.request) {
			// A native view's current receiver, not its borrowed payload,
			// proves the output source. Source constructors likewise use
			// their current inputs or authorized currentWorkspace capture.
			if observation.request.ReceiverTypeName == "Workspace" {
				for _, binding := range c.matchingBindings(observation.request.Receiver, observation.bindings) {
					fact.Retained = append(fact.Retained, workspaceRetainedInput{Input: binding.path, Workspace: binding.workspace})
				}
			} else {
				fact.Retained = append(fact.Retained, workspaceRetainedInput{Workspace: observation.boundary})
			}
		} else {
			err = c.workspaceValues(value, nil, true, visitWorkspace)
		}
		if err != nil {
			fact.CapabilitiesComplete = false
			fact.Prepared.incomplete = true
			fact.Issues = append(fact.Issues, "cannot inspect returned source metadata: "+err.Error())
		}
		if c.capabilitiesIncomplete {
			fact.CapabilitiesComplete = false
			fact.Issues = append(fact.Issues, "returned native value has unresolved retained inputs")
		}
		if _, workspace := dagql.UnwrapAs[*Workspace](value); workspace {
			for _, retained := range fact.Retained {
				if len(retained.Output) == 0 && len(retained.Input) != 0 {
					fact.Boundary = retained.Workspace
					break
				}
			}
			if fact.Boundary == "" && observation.boundary != "" && !observation.storedField {
				fact.Boundary = observation.boundary
			}
			view := observation.request.Module == nil && observation.request.ReceiverTypeName == "Workspace" && workspaceInputView(observation.request)
			if fact.Boundary == "" && view {
				fact.CapabilitiesComplete = false
				fact.Issues = append(fact.Issues, "Workspace view has no complete receiver source binding")
			}
			if fact.Boundary == "" && observation.request.Module == nil && !observation.storedField && observation.request.Field != "currentWorkspace" && !view {
				recipe, err := observation.request.ResultCall.RecipeDigest(ctx)
				if err != nil {
					return workspaceInputFact{}, err
				}
				fact.Boundary = "workspace-producer:" + recipe.String()
			}
			if fact.Boundary == "" {
				fact.CapabilitiesComplete = false
				fact.Prepared.incomplete = true
				fact.Issues = append(fact.Issues, "Workspace output has no complete source boundary")
			}
			for i := range fact.Retained {
				if len(fact.Retained[i].Output) == 0 && len(fact.Retained[i].Input) == 0 {
					fact.Retained[i].Workspace = fact.Boundary
				}
			}
		}
	}
	observation.reads.mu.Lock()
	fact.Complete = !observation.reads.incompleteLocked() && !fact.Prepared.incomplete
	observation.reads.mu.Unlock()
	fact.Issues = append(fact.Issues, observation.summary().Issues...)
	validateWorkspaceRetainedBindings(&fact)
	observation.recordTelemetry(fact, fact.Complete)
	return fact, nil
}

func replaceWorkspaceInputFact(facts []dagql.ResultFact, fact workspaceInputFact) []dagql.ResultFact {
	out := slices.DeleteFunc(slices.Clone(facts), func(record dagql.ResultFact) bool { return record.Namespace == workspaceInputsNamespace })
	payload, _ := json.Marshal(fact)
	return append(out, dagql.ResultFact{Namespace: workspaceInputsNamespace, Payload: payload})
}

func (observation *workspaceInputCall) captureFacts(ctx context.Context, value dagql.AnyResult) error {
	if observation.request.Module == nil && observation.request.ReceiverTypeName == "Query" && observation.request.Field == "currentWorkspace" {
		var err error
		if bound, ok := WorkspaceFromContext(ctx); ok {
			observation.boundary, err = workspaceResultBoundary(ctx, bound)
		} else if workspace, ok := dagql.UnwrapAs[*Workspace](value); ok {
			// The resolver has already authorized and returned the actual
			// current source. Capture it before equal payload reuse.
			observation.boundary, err = actualWorkspaceBoundary(ctx, workspace)
		}
		if err != nil {
			observation.initial.Issues = append(observation.initial.Issues, "cannot determine current Workspace source boundary: "+err.Error())
			observation.initial.incomplete = true
		}
	}
	fact, err := observation.capture(ctx, value)
	if err != nil {
		return err
	}
	observation.request.ResultFacts = replaceWorkspaceInputFact(observation.request.ResultFacts, fact)
	observation.request.ResultFactsPrepared = true
	if observation.storedField {
		return observation.useStoredFieldCapture(ctx, fact)
	}
	return nil
}

func (observation *workspaceInputCall) useStoredFieldCapture(ctx context.Context, fact workspaceInputFact) error {
	c, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		return err
	}
	current := c.materializeReads(fact, observation.request.ResultCall).normalized()
	current.incomplete = current.incomplete || observation.request.ResultFactsIncomplete
	observation.reused = &current
	return nil
}

func (observation *workspaceInputCall) rebindFacts(ctx context.Context, previous *dagql.ResultCall, value dagql.AnyResult, stored []dagql.ResultFact, complete bool) ([]dagql.ResultFact, bool, error) {
	if observation.storedField {
		// Attachment can populate the field recipe without observing a read.
		// Current owner inputs define this read, including on an exact hit.
		fact, err := observation.capture(ctx, value)
		if err != nil {
			return nil, false, err
		}
		if err := observation.useStoredFieldCapture(ctx, fact); err != nil {
			return nil, false, err
		}
		return replaceWorkspaceInputFact(observation.request.ResultFacts, fact), fact.Complete, nil
	}
	if previous != nil {
		oldRecipe, oldErr := previous.RecipeDigest(ctx)
		currentRecipe, currentErr := observation.request.ResultCall.RecipeDigest(ctx)
		if oldErr == nil && currentErr == nil && oldRecipe == currentRecipe {
			if facts, covered, found, err := observation.rebindExactFacts(ctx, stored, complete); found || err != nil {
				return facts, covered, err
			}
			// A nested source call can leave this scratch recipe without facts.
			// Only fill an absent capture on an exact, fully covered result.
			if complete && len(stored) == 0 && workspaceInputEmptyDirectory(ctx, observation.request) {
				fact, err := observation.capture(ctx, value)
				return replaceWorkspaceInputFact(observation.request.ResultFacts, fact), fact.Complete, err
			}
		}
	}
	var rules []workspaceFindUpRule
	var contextInputs workspaceInputSummary
	operation := workspaceInputOperation(ctx, observation.request.ResultCall)
	for _, record := range stored {
		if record.Namespace != workspaceInputsNamespace {
			continue
		}
		var old workspaceInputFact
		if err := json.Unmarshal(record.Payload, &old); err != nil {
			return nil, false, err
		}
		if old.Operation == operation {
			rules = append(rules, old.FindUps...)
			contextInputs.merge(old.ContextInputs)
		}
	}
	observation.reads.addContextInputs(contextInputs)
	rules = observation.rebindFindUps(ctx, rules)
	observation.reads.add(workspaceInputSummary{FindUps: rules, findUps: rules})
	if observation.prepared {
		fact, err := observation.capture(ctx, value)
		return replaceWorkspaceInputFact(observation.request.ResultFacts, fact), complete && fact.Complete, err
	}
	fact := workspaceInputFact{Operation: workspaceInputOperation(ctx, observation.request.ResultCall), Prepared: observation.preparedOrigins(), ContextInputs: observation.summary().capturedContextInputs()}
	if err := observation.currentRuntimeInputs(ctx, &fact.Prepared); err != nil {
		return nil, false, err
	}
	for _, record := range stored {
		if record.Namespace != workspaceInputsNamespace {
			if slices.Contains(legacyWorkspaceInputsNamespaces[:], record.Namespace) {
				fact.Issues = append(fact.Issues, workspaceLegacyIssues(record.Payload)...)
			}
			continue
		}
		var old workspaceInputFact
		if err := json.Unmarshal(record.Payload, &old); err != nil {
			return nil, false, err
		}
		if old.Operation != fact.Operation {
			continue
		}
		fact.Complete = old.Complete && !fact.Prepared.incomplete
		fact.CapabilitiesComplete = old.CapabilitiesComplete
		fact.Reads = append(fact.Reads, old.Reads...)
		fact.Issues = append(fact.Issues, old.Issues...)
		for _, retained := range old.Retained {
			binding, err := workspaceFindBinding(observation.bindings, retained.Input)
			if err != nil {
				fact.CapabilitiesComplete = false
				fact.Issues = append(fact.Issues, err.Error())
				continue
			}
			retained.Workspace = binding.workspace
			fact.Retained = append(fact.Retained, retained)
			if old.Boundary != "" && len(retained.Output) == 0 {
				fact.Boundary = binding.workspace
			}
		}
	}
	if !fact.Complete {
		fact.Issues = append(fact.Issues, "cached call has no complete bound source read capture for "+fact.Operation)
	}
	fact.FindUps = observation.summary().findUps
	live := observation.reads.snapshot()
	fact.Complete = fact.Complete && !live.incomplete
	fact.Issues = append(fact.Issues, live.Issues...)
	validateWorkspaceRetainedBindings(&fact)
	observation.recordTelemetry(fact, complete && fact.Complete)
	return replaceWorkspaceInputFact(observation.request.ResultFacts, fact), complete && fact.Complete, nil
}

// The native Query.directory constructor has no Workspace input. Its only
// implicit input is the engine platform, which the collector still visits.
func workspaceInputEmptyDirectory(ctx context.Context, req *dagql.CallRequest) bool {
	if req.Kind != dagql.ResultCallKindField || req.Module != nil || req.Receiver != nil ||
		req.ReceiverTypeName != "Query" || req.Field != "directory" || len(req.Args) != 0 ||
		req.Nth != 0 || req.ListItem || req.Type == nil ||
		*req.Type != (dagql.ResultCallType{NamedType: "Directory", NonNull: true}) || len(req.ImplicitInputs) != 1 {
		return false
	}
	input := req.ImplicitInputs[0]
	if input == nil || input.Name != "engineDefaultPlatform" || input.Value == nil ||
		input.Value.Kind != dagql.ResultCallLiteralKindString {
		return false
	}
	query, err := CurrentQuery(ctx)
	return err == nil && input.Value.StringValue == query.Platform().Format()
}

// Identical inputs retain their owned capture, including runtime setup that
// is not reconstructed from the returned payload. Only ambient body searches
// take the current caller's Workspace binding.
func (observation *workspaceInputCall) rebindExactFacts(ctx context.Context, stored []dagql.ResultFact, complete bool) ([]dagql.ResultFact, bool, bool, error) {
	c, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		return nil, false, false, err
	}
	operation := workspaceInputOperation(ctx, observation.request.ResultCall)
	facts := slices.Clone(stored)
	summary := workspaceInputSummary{incomplete: !complete}
	found := false
	var wire *workspaceInputTelemetry
	if observation.telemetryEnabled.Load() {
		wire = newWorkspaceInputTelemetry()
	}
	for i, record := range facts {
		if record.Namespace != workspaceInputsNamespace {
			continue
		}
		var fact workspaceInputFact
		if err := json.Unmarshal(record.Payload, &fact); err != nil {
			return nil, false, false, err
		}
		if fact.Operation != operation {
			continue
		}
		found = true
		rules, err := bindWorkspaceFindUps(ctx, fact.FindUps)
		if err != nil {
			fact.Issues = append(fact.Issues, "cannot bind Host.findUp filtering rule to caller Workspace: "+err.Error())
			fact.Complete = false
		}
		fact.FindUps = rules
		summary.merge(c.materializeReads(fact, observation.request.ResultCall))
		summary.mergeContextInputEvents(&fact.ContextInputs)
		summary.findUps = append(summary.findUps, fact.FindUps...)
		complete = complete && fact.Complete
		if wire != nil {
			wire.fact(fact, observation.bindings)
		}
		facts[i].Payload, err = json.Marshal(fact)
		if err != nil {
			return nil, false, false, err
		}
	}
	if found {
		current := summary.normalized()
		observation.reused = &current
		if wire != nil {
			wire.complete = wire.complete && complete
			observation.telemetry.Store(wire)
		}
	}
	return facts, complete, found, nil
}

func (observation *workspaceInputCall) currentRuntimeInputs(ctx context.Context, prepared *workspaceInputSummary) error {
	if observation.request.Module == nil {
		return nil
	}
	collector, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		return err
	}
	collector.evaluating = true
	collector.moduleInputs(observation.request.Module.ResultRef)
	prepared.merge(collector.result)
	prepared.reads = nil
	prepared.findUps = nil
	return nil
}

func validateWorkspaceRetainedBindings(fact *workspaceInputFact) {
	outputs := map[string]string{}
	for _, retained := range fact.Retained {
		key := workspacePathKey(retained.Output)
		if previous, exists := outputs[key]; exists && previous != retained.Workspace {
			fact.CapabilitiesComplete = false
			fact.Issues = append(fact.Issues, "retained Workspace has ambiguous source bindings")
			if len(retained.Output) == 0 {
				fact.Boundary = ""
			}
		}
		outputs[key] = retained.Workspace
	}
}
