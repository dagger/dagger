package dagql

import (
	"context"

	enginetel "github.com/dagger/dagger/engine/telemetry"
)

// recordWorkloadUse reports to the workload export that the caller's session
// needed this evaluated result. A demand for only the metadata part, such as
// a container's configuration, does not need the result's deferred work, so
// it is not a use. The digest is read after the evaluation, when the result
// has learned its content, so it matches what later cache hits report.
func (c *Cache) recordWorkloadUse(ctx context.Context, shared *sharedResult, parts []PartKey) {
	if len(parts) > 0 {
		metadataOnly := true
		for _, part := range parts {
			if part != "metadata" {
				metadataOnly = false
				break
			}
		}
		if metadataOnly {
			return
		}
	}
	enginetel.RecordWorkloadUse(ctx, uint64(shared.id), func() string {
		frame := shared.loadResultCall()
		if frame == nil {
			return ""
		}
		if content := frame.ContentDigest(); content != "" {
			return content.String()
		}
		dig, err := frame.contentPreferredDigestForTelemetry(c)
		if err != nil {
			return ""
		}
		return dig.String()
	})
}
