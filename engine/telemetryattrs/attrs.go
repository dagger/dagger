package telemetryattrs

const (
	UIResumeOutputAttr = "dagger.io/ui.resume.output"

	// Streaming progress over OTel logs.
	//
	// A log record carrying ProgressItemAttr is progress data, not log text:
	// it reports absolute completion for one named item of work (a layer
	// being fetched, a file being transferred) within the span the record is
	// attached to. The TUI folds these records into progress bars instead of
	// rendering them as logs.
	//
	// Records are keyed by (span, item): each new record replaces the item's
	// previous state, so emitters can throttle freely and consumers only keep
	// the latest values.

	// ProgressItemAttr uniquely names the item within its span, e.g. a layer
	// digest. (string)
	ProgressItemAttr = "dagger.io/progress.item"
	// ProgressCurrentAttr is the item's absolute completed amount. (int64)
	ProgressCurrentAttr = "dagger.io/progress.current"
	// ProgressTotalAttr is the item's expected final amount. Zero or absent
	// means the total is unknown (indeterminate). (int64)
	ProgressTotalAttr = "dagger.io/progress.total"
	// ProgressUnitAttr optionally names the unit of current/total, e.g.
	// "bytes", for human-readable display. (string)
	ProgressUnitAttr = "dagger.io/progress.unit"
)
