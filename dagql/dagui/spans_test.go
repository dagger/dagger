package dagui

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TestRollUpStateIncremental verifies that rollup state updates are incremental
// and correct across various state transitions
func TestRollUpStateIncremental(t *testing.T) {
	db := NewDB()

	// Create a parent span that will track rollup state
	parentID := SpanID{trace.SpanID{1}}
	parent := db.newSpan(parentID)
	parent.Received = true
	parent.RollUpSpans = true
	parent.StartTime = time.Now()
	parent.EndTime = parent.StartTime.Add(-1) // running
	db.Spans.Add(parent)
	db.integrateSpan(parent)

	// Helper to create a child span
	createChild := func(id byte) *Span {
		childID := SpanID{trace.SpanID{id}}
		child := db.newSpan(childID)
		child.Received = true
		child.ParentID = parentID
		child.ParentSpan = parent
		child.StartTime = time.Now()
		child.EndTime = child.StartTime.Add(-1) // running by default
		db.Spans.Add(child)
		db.integrateSpan(child)
		return child
	}

	// Helper to transition a span to a specific state
	transitionTo := func(span *Span, state string) {
		now := time.Now()
		switch state {
		case "completed":
			span.EndTime = now
			span.Status = sdktrace.Status{Code: codes.Ok}
		case "failed":
			span.EndTime = now
			span.Status = sdktrace.Status{Code: codes.Error}
		case "cached":
			span.EndTime = now
			span.Status = sdktrace.Status{Code: codes.Ok}
			span.Cached = true
		case "canceled":
			span.EndTime = now
			span.Canceled = true
		case "pending":
			span.EndTime = now
			span.Status = sdktrace.Status{Code: codes.Ok}
			span.EffectIDs = []string{"pending-effect"}
		}
		span.PropagateStatusToParentsAndLinks()
	}

	// Test 1: Initial state - parent should be running
	if parent.rollUpState == nil {
		parent.rollUpState = &RollUpState{}
	}
	if parent.rollUpState.RunningCount != 0 {
		t.Errorf("Expected parent RunningCount=0, got %d", parent.rollUpState.RunningCount)
	}

	// Test 2: Add a running child
	child1 := createChild(2)
	child1.PropagateStatusToParentsAndLinks()

	if parent.rollUpState.RunningCount != 1 {
		t.Errorf("Expected parent RunningCount=1 after adding running child, got %d", parent.rollUpState.RunningCount)
	}

	// Test 3: Add more running children
	child2 := createChild(3)
	child2.PropagateStatusToParentsAndLinks()

	child3 := createChild(4)
	child3.PropagateStatusToParentsAndLinks()

	if parent.rollUpState.RunningCount != 3 {
		t.Errorf("Expected parent RunningCount=3, got %d", parent.rollUpState.RunningCount)
	}

	// Test 4: Transition child1 to completed
	transitionTo(child1, "completed")

	if parent.rollUpState.RunningCount != 2 {
		t.Errorf("Expected parent RunningCount=2 after child1 completed, got %d", parent.rollUpState.RunningCount)
	}
	if parent.rollUpState.SuccessCount != 1 {
		t.Errorf("Expected parent SuccessCount=1 after child1 completed, got %d", parent.rollUpState.SuccessCount)
	}

	// Test 5: Transition child2 to failed
	transitionTo(child2, "failed")

	if parent.rollUpState.RunningCount != 1 {
		t.Errorf("Expected parent RunningCount=1 after child2 failed, got %d", parent.rollUpState.RunningCount)
	}
	if parent.rollUpState.FailedCount != 1 {
		t.Errorf("Expected parent FailedCount=1 after child2 failed, got %d", parent.rollUpState.FailedCount)
	}

	// Test 6: Transition child3 to cached
	transitionTo(child3, "cached")

	if parent.rollUpState.RunningCount != 0 {
		t.Errorf("Expected parent RunningCount=0 after all children finished, got %d", parent.rollUpState.RunningCount)
	}
	if parent.rollUpState.CachedCount != 1 {
		t.Errorf("Expected parent CachedCount=1 after child3 cached, got %d", parent.rollUpState.CachedCount)
	}

	// Test 7: Add a canceled child
	child4 := createChild(5)
	transitionTo(child4, "canceled")

	if parent.rollUpState.CanceledCount != 1 {
		t.Errorf("Expected parent CanceledCount=1, got %d", parent.rollUpState.CanceledCount)
	}

	// Test 8: Add a pending child
	child5 := createChild(6)
	transitionTo(child5, "pending")

	if parent.rollUpState.PendingCount != 1 {
		t.Errorf("Expected parent PendingCount=1, got %d", parent.rollUpState.PendingCount)
	}

	// Final verification: total counts
	total := parent.rollUpState.RunningCount +
		parent.rollUpState.PendingCount +
		parent.rollUpState.CachedCount +
		parent.rollUpState.SuccessCount +
		parent.rollUpState.FailedCount +
		parent.rollUpState.CanceledCount

	if total != 5 {
		t.Errorf("Expected total count=5, got %d (running=%d, pending=%d, cached=%d, success=%d, failed=%d, canceled=%d)",
			total,
			parent.rollUpState.RunningCount,
			parent.rollUpState.PendingCount,
			parent.rollUpState.CachedCount,
			parent.rollUpState.SuccessCount,
			parent.rollUpState.FailedCount,
			parent.rollUpState.CanceledCount,
		)
	}
}

// TestRollUpStateMultiLevel verifies that rollup state propagates correctly
// through multiple levels of the span hierarchy
func TestRollUpStateMultiLevel(t *testing.T) {
	db := NewDB()

	// Create a three-level hierarchy:
	// grandparent -> parent -> child
	grandparentID := SpanID{trace.SpanID{1}}
	grandparent := db.newSpan(grandparentID)
	grandparent.Received = true
	grandparent.RollUpSpans = true
	grandparent.StartTime = time.Now()
	grandparent.EndTime = grandparent.StartTime.Add(-1)
	db.Spans.Add(grandparent)
	db.integrateSpan(grandparent)

	parentID := SpanID{trace.SpanID{2}}
	parent := db.newSpan(parentID)
	parent.Received = true
	parent.ParentID = grandparentID
	parent.ParentSpan = grandparent
	parent.RollUpSpans = true
	parent.StartTime = time.Now()
	parent.EndTime = parent.StartTime.Add(-1)
	db.Spans.Add(parent)
	db.integrateSpan(parent)

	childID := SpanID{trace.SpanID{3}}
	child := db.newSpan(childID)
	child.Received = true
	child.ParentID = parentID
	child.ParentSpan = parent
	child.StartTime = time.Now()
	child.EndTime = child.StartTime.Add(-1)
	db.Spans.Add(child)
	db.integrateSpan(child)

	// Ensure rollup states are initialized
	if grandparent.rollUpState == nil {
		grandparent.rollUpState = &RollUpState{}
	}
	if parent.rollUpState == nil {
		parent.rollUpState = &RollUpState{}
	}

	// Propagate child's state
	child.PropagateStatusToParentsAndLinks()

	// Both parent and grandparent should count the running child
	if parent.rollUpState.RunningCount != 1 {
		t.Errorf("Expected parent RunningCount=1, got %d", parent.rollUpState.RunningCount)
	}
	if grandparent.rollUpState.RunningCount != 1 {
		t.Errorf("Expected grandparent RunningCount=1, got %d", grandparent.rollUpState.RunningCount)
	}

	// Complete the child
	child.EndTime = time.Now()
	child.Status = sdktrace.Status{Code: codes.Ok}
	child.PropagateStatusToParentsAndLinks()

	// Both ancestors should reflect the change
	if parent.rollUpState.RunningCount != 0 {
		t.Errorf("Expected parent RunningCount=0 after child completed, got %d", parent.rollUpState.RunningCount)
	}
	if parent.rollUpState.SuccessCount != 1 {
		t.Errorf("Expected parent SuccessCount=1 after child completed, got %d", parent.rollUpState.SuccessCount)
	}

	if grandparent.rollUpState.RunningCount != 0 {
		t.Errorf("Expected grandparent RunningCount=0 after child completed, got %d", grandparent.rollUpState.RunningCount)
	}
	if grandparent.rollUpState.SuccessCount != 1 {
		t.Errorf("Expected grandparent SuccessCount=1 after child completed, got %d", grandparent.rollUpState.SuccessCount)
	}
}

// TestRollUpStateWorksForAllSpans verifies that rollup state tracking works
// for all spans, not just those marked with RollUpSpans=true
func TestRollUpStateWorksForAllSpans(t *testing.T) {
	db := NewDB()

	// Create a parent span WITHOUT RollUpSpans flag
	parentID := SpanID{trace.SpanID{1}}
	parent := db.newSpan(parentID)
	parent.Received = true
	parent.RollUpSpans = false // explicitly not a rollup span
	parent.StartTime = time.Now()
	parent.EndTime = parent.StartTime.Add(-1)
	db.Spans.Add(parent)
	db.integrateSpan(parent)

	// Ensure rollup state is initialized
	if parent.rollUpState == nil {
		parent.rollUpState = &RollUpState{}
	}

	// Create a child
	childID := SpanID{trace.SpanID{2}}
	child := db.newSpan(childID)
	child.Received = true
	child.ParentID = parentID
	child.ParentSpan = parent
	child.StartTime = time.Now()
	child.EndTime = child.StartTime.Add(-1)
	db.Spans.Add(child)
	db.integrateSpan(child)
	child.PropagateStatusToParentsAndLinks()

	// Even though parent is not marked as RollUpSpans, it should still track counts
	if parent.rollUpState.RunningCount != 1 {
		t.Errorf("Expected parent RunningCount=1 even without RollUpSpans flag, got %d", parent.rollUpState.RunningCount)
	}

	// This allows any span to be used for rollup rendering if needed
	state := parent.RollUpState()
	if state == nil {
		t.Error("Expected non-nil rollup state for non-rollup span")
	}
}
