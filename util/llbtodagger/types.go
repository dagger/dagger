package llbtodagger

import (
	"fmt"

	"github.com/opencontainers/go-digest"
)

// UnsupportedOpError is returned when an LLB op cannot be mapped faithfully to
// a Dagger API ID.
type UnsupportedOpError struct {
	OpDigest digest.Digest
	OpType   string
	Reason   string
}

func (e *UnsupportedOpError) Error() string {
	switch {
	case e == nil:
		return "<nil>"
	case e.OpDigest == "":
		return fmt.Sprintf("llbtodagger: unsupported op %q: %s", e.OpType, e.Reason)
	default:
		return fmt.Sprintf("llbtodagger: unsupported op %q (%s): %s", e.OpType, e.OpDigest, e.Reason)
	}
}

func unsupported(opDigest digest.Digest, opType, reason string) error {
	return &UnsupportedOpError{
		OpDigest: opDigest,
		OpType:   opType,
		Reason:   reason,
	}
}
