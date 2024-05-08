package telemetry

// The following attributes are used by the UI to interpret spans and control
// their behavior in the UI.
const (
	// The base64-encoded, protobuf-marshalled callpbv1.Call that this span
	// represents.
	DagCallAttr = "dagger.io/dag.call"

	// The digest of the protobuf-marshalled Call that this span represents.
	//
	// This value acts as a node ID in the conceptual DAG.
	DagDigestAttr = "dagger.io/dag.digest"

	// The digest of this call without considering any calls passed to
	// it as arguments.
	//
	// This value is used to measure performance of function calls over
	// time across different input values (i.e. source code).
	DagDigestStableAttr = "dagger.io/dag.digest.stable"

	// The name of the module that provided the function that this span
	// is calling.
	DagModuleNameAttr = "dagger.io/dag.module.name"

	// The ref of the module that provided the function that this span
	// is calling.
	DagModuleRefAttr = "dagger.io/dag.module.ref"

	// The digest of the module that provided the function that this span
	// is calling.
	DagModuleDigestAttr = "dagger.io/dag.module.digest"

	// Indicates what type of caller resulted in this span's execution.
	//
	// One of: module, internal, direct.
	DagCallerType = "dagger.io/dag.caller.type"

	// The list of DAG digests that the span depends on.
	//
	// This is not currently used by the UI, but it could be used to drive higher
	// level DAG walking processes without having to unmarshal the full call.
	DagInputsAttr = "dagger.io/dag.inputs"

	// The DAG call digest that the call returned, if the call returned an
	// Object.
	//
	// This information is used to simplify values in the UI by showing their
	// highest-level creator. For example, if foo().bar() returns a().b().c(), we
	// will show foo().bar() instead of a().b().c() as it will be a more
	// recognizable value to the user.
	DagOutputAttr = "dagger.io/dag.output"

	// Indicates that this span is "internal" and can be hidden by default.
	//
	// Internal spans may typically be revealed with a toggle.
	UIInternalAttr = "dagger.io/ui.internal"

	// Hide child spans by default.
	UIEncapsulateAttr = "dagger.io/ui.encapsulate"

	// Substitute the span for its children and move its logs to its parent.
	UIPassthroughAttr = "dagger.io/ui.passthrough" //nolint: gosec // lol

	// Causes the parent span to act as if Passthrough was set.
	UIMaskAttr = "dagger.io/ui.mask"

	// NB: the following attributes are not currently used.

	// Indicates that this span was a cache hit and did nothing.
	CachedAttr = "dagger.io/dag.cached"

	// Indicates that this span was interrupted.
	CanceledAttr = "dagger.io/dag.canceled"

	// The base64-encoded, protobuf-marshalled Buildkit LLB op payload that this
	// span represents.
	LLBOpAttr = "dagger.io/llb.op"

	// The digests of the LLB operations that this span depends on, allowing the
	// UI to attribute their future "cost."
	LLBDigestsAttr = "dagger.io/llb.digests"

	// The amount of progress that needs to be reached.
	ProgressTotalAttr = "dagger.io/progress.total"

	// Current value for the progress.
	ProgressCurrentAttr = "dagger.io/progress.current"

	// Indicates the units for the progress numbers.
	ProgressUnitsAttr = "dagger.io/progress.units"
)
