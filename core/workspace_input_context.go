package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
)

// fixedWorkspaceContextInputs keeps the origins of an input actually consumed
// by native code. These are producer-owned inputs, not caller-bound read events.
func fixedWorkspaceContextInputs(inputs workspaceInputSummary) workspaceInputSummary {
	inputs.reads = nil
	inputs.findUps = nil
	inputs.contextInputs = nil
	inputs.Sources = append([]workspaceInputSource(nil), inputs.Sources...)
	for i := range inputs.Sources {
		inputs.Sources[i].producer = nil
	}
	return inputs.normalized()
}

func (s *workspaceInputSummary) mergeContextInputEvents(inputs *workspaceInputSummary) {
	if inputs == nil {
		return
	}
	fixed := fixedWorkspaceContextInputs(*inputs)
	if len(fixed.Sources) == 0 && len(fixed.FindUps) == 0 && len(fixed.Issues) == 0 && !fixed.incomplete {
		return
	}
	if s.contextInputs != nil {
		previous := fixedWorkspaceContextInputs(*s.contextInputs)
		previous.merge(fixed)
		fixed = previous.normalized()
	}
	s.contextInputs = &fixed
}

func (s *workspaceInputSummary) addContextInputs(inputs workspaceInputSummary) {
	fixed := fixedWorkspaceContextInputs(inputs)
	if len(fixed.Sources) == 0 && len(fixed.FindUps) == 0 && len(fixed.Issues) == 0 && !fixed.incomplete {
		return
	}
	s.merge(fixed)
	s.mergeContextInputEvents(&fixed)
}

func (s workspaceInputSummary) capturedContextInputs() workspaceInputSummary {
	if s.contextInputs == nil {
		return workspaceInputSummary{}
	}
	return fixedWorkspaceContextInputs(*s.contextInputs)
}

func (r *workspaceInputReads) addContextInputs(inputs workspaceInputSummary) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.summary.addContextInputs(inputs)
}

// Call this only where native code consumes an exact context input. Importing
// its facts alone does not authorize its producer ancestry as body inputs.
func recordWorkspaceContextInput(ctx context.Context, value dagql.AnyResult) {
	reads := currentWorkspaceInputReads(ctx)
	if reads == nil {
		return
	}
	fail := func(issue string) {
		reads.addContextInputs(workspaceInputSummary{Issues: []string{issue}, incomplete: true})
	}
	if value == nil || value.Unwrap() == nil {
		fail("consumed native context input has no value")
		return
	}
	id, err := value.ID()
	if err != nil || id == nil || id.EngineResultID() == 0 {
		fail("consumed native context input has no attached result ID")
		return
	}
	collector, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		fail("cannot inspect consumed native context input: " + err.Error())
		return
	}
	ref := &dagql.ResultCallRef{ResultID: id.EngineResultID()}
	collector.ref(ref)
	reads.mu.Lock()
	defer reads.mu.Unlock()
	reads.summary.addContextInputs(collector.result)
	if reads.values == nil {
		reads.values = map[uint64]*dagql.ResultCallRef{}
	}
	reads.values[ref.ResultID] = ref
}
