package telemetry

const (
	DagDigestAttr = "dagger.io/dag.digest"

	DagInputsAttr = "dagger.io/dag.inputs"
	DagOutputAttr = "dagger.io/dag.output"

	CachedAttr   = "dagger.io/dag.cached"
	CanceledAttr = "dagger.io/dag.canceled"
	InternalAttr = "dagger.io/dag.internal"

	DagCallAttr = "dagger.io/dag.call"

	LLBOpAttr = "dagger.io/llb.op"

	// Hide child spans by default.
	UIEncapsulateAttr = "dagger.io/ui.encapsulate"

	// The following are theoretical, if/when we want to express the same
	// concepts from Progrock.

	// The parent span of this task. Might not need this at all, if we want to
	// just rely on span parent, but the thinking is the span parent could be
	// pretty brittle.
	TaskParentAttr = "dagger.io/task.parent"

	// Progress bars.
	ProgressCurrentAttr = "dagger.io/progress.current"
	ProgressTotalAttr   = "dagger.io/progress.total"
)
