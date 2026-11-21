package clientdb

// hasDisplay reports whether any of spanID's log rows is display text.
func (l *logLookup) hasDisplay(spanID string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	span, ok := l.spans.lookup(spanID)
	if !ok {
		return false
	}
	for _, row := range l.rowsBySpan[span] {
		if i := row - 1; i >= 0 && int(i) < len(l.meta) && l.meta[i].flags().display() {
			return true
		}
	}
	return false
}
