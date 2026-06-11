package telemetryattrs

const (
	UIResumeOutputAttr = "dagger.io/ui.resume.output"

	// DagBlockedAttr marks a lazy-evaluation resume span that aborted because a
	// prerequisite result's evaluation failed, rather than because the result's
	// own deferred work failed. The UI treats a blocked resumption as if the
	// deferred work never ran: the owning API spans return to pending instead
	// of being marked caused-failed.
	DagBlockedAttr = "dagger.io/dag.blocked"
)
