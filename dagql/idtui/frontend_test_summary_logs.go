package idtui

import (
	"fmt"

	"github.com/dagger/dagger/dagql/dagui"
	telemetry "github.com/dagger/otel-go"
	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

func appendTestSummaryLogRecords(logs map[dagui.SpanID]*Vterm, profile termenv.Profile, spanID dagui.SpanID, records []sdklog.Record) {
	if logs == nil || !spanID.IsValid() {
		return
	}
	for _, record := range records {
		contentType, skip := testSummaryLogRecordInfo(record)
		if skip {
			continue
		}
		body := record.Body().AsString()
		if body == "" {
			continue
		}
		vt := logs[spanID]
		if vt == nil {
			vt = NewVterm(profile)
			logs[spanID] = vt
		}
		if contentType == "text/markdown" {
			_, _ = vt.WriteMarkdown([]byte(body))
		} else {
			_, _ = fmt.Fprint(vt, body)
		}
	}
}

func testSummaryLogRecordInfo(record sdklog.Record) (contentType string, skip bool) {
	record.WalkAttributes(func(kv log.KeyValue) bool {
		switch kv.Key {
		case telemetry.ContentTypeAttr:
			contentType = kv.Value.AsString()
		case telemetry.StdioEOFAttr, telemetry.LogsVerboseAttr:
			if kv.Value.AsBool() {
				skip = true
				return false
			}
		}
		return true
	})
	return contentType, skip
}
