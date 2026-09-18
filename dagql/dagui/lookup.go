package dagui

// Name-based span lookup, shared by the CLI's --check/--test selectors
// ('dagger trace' zooms to the result, 'dagger cloud logs' streams its logs).
// Both prefer a FAILED match over a passing one, so the hint a failing report
// prints resolves to the failure the user is chasing rather than a passing
// retry that shares the name.

// FindCheckSpan returns the span of the check named name, or nil.
func (db *DB) FindCheckSpan(name string) *Span {
	return db.findSpan(func(span *Span) bool {
		return span.CheckName == name
	})
}

// FindTestSpan returns the span of the test case named name, or nil. It
// matches the OTel test case name, an optional "<suite> <case>" qualification,
// or the span name.
func (db *DB) FindTestSpan(name string) *Span {
	return db.findSpan(func(span *Span) bool {
		return span.TestCaseName != "" &&
			(span.TestCaseName == name ||
				span.TestSuiteName+" "+span.TestCaseName == name ||
				span.Name == name)
	})
}

func (db *DB) findSpan(pred func(*Span) bool) *Span {
	var fallback *Span
	for _, span := range db.Spans.Order {
		if !pred(span) {
			continue
		}
		if span.IsFailed() {
			return span
		}
		if fallback == nil {
			fallback = span
		}
	}
	return fallback
}
