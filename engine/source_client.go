package engine

import "fmt"

// SourceClientUnavailableError reports that a source client published its
// attachables successfully but is no longer available.
type SourceClientUnavailableError struct {
	ClientID string
}

func (err *SourceClientUnavailableError) Error() string {
	return fmt.Sprintf("source client %q is unavailable", err.ClientID)
}
