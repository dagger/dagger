package tracing

const (
	DagDigestAttr = "dagger.io/dag.digest"

	DagInputsAttr = "dagger.io/dag.inputs"
	DagOutputAttr = "dagger.io/dag.output"

	CachedAttr   = "dagger.io/dag.cached"
	CanceledAttr = "dagger.io/dag.canceled"
	InternalAttr = "dagger.io/dag.internal"

	TaskParentAttr = "task.parent" // TODO remove

	// TODO remove, or ideally use someday (e.g. image fetching)
	ProgressCurrentAttr = "dagger.io/progress.current"
	ProgressTotalAttr   = "dagger.io/progress.total"

	DagIDBlobAttr = "dagger.io/id.blob"
	DagIDTypeAttr = "dagger.io/id.type"

	LLBOpBlobAttr = "dagger.io/op.blob"
	LLBOpTypeAttr = "dagger.io/op.type"

	UIPrimaryAttr     = "dagger.io/ui.primary"
	UIEncapsulateAttr = "dagger.io/ui.encapsulate"
)
