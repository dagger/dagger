package dagui

import (
	"strings"

	"github.com/dagger/dagger/engine/telemetryattrs"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// MediaRecord is an inline attachment at a log record's position in the output.
// Data contains standard base64, not display text. Frontends that cannot render
// the attachment must use LogBodyString instead. Decoding and resource limits
// belong to the renderer, so merely ingesting a record never allocates its bytes.
type MediaRecord struct {
	Kind     string
	MIMEType string
	Data     string
}

// ParseMediaRecord recognizes typed media attributes without coercing malformed
// values. Its Body remains ordinary safe text for plain/log/ascii frontends.
// A capable frontend may use Body as a caption alongside the attachment; Data
// must never be passed to a text renderer.
func ParseMediaRecord(record sdklog.Record) (MediaRecord, bool) {
	body, ok := LogBodyString(record)
	if !ok || body == "" {
		return MediaRecord{}, false
	}
	var media MediaRecord
	valid := true
	record.WalkAttributes(func(kv otellog.KeyValue) bool {
		var dst *string
		switch kv.Key {
		case telemetryattrs.LogMediaKindAttr:
			dst = &media.Kind
		case telemetryattrs.LogMediaMIMETypeAttr:
			dst = &media.MIMEType
		case telemetryattrs.LogMediaDataAttr:
			dst = &media.Data
		default:
			return true
		}
		value, ok := LogValueString(kv.Value)
		if !ok {
			valid = false
		} else {
			*dst = value
		}
		return true
	})
	if !valid || media.Data == "" {
		return MediaRecord{}, false
	}
	switch media.Kind {
	case "image", "audio":
		valid = strings.HasPrefix(media.MIMEType, media.Kind+"/") && len(media.MIMEType) > len(media.Kind)+1
	case "document":
		valid = media.MIMEType == "application/pdf"
	default:
		valid = false
	}
	if !valid {
		return MediaRecord{}, false
	}
	return media, true
}
