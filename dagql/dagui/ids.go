package dagui

import (
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel/trace"
)

type SpanContext struct {
	TraceID TraceID
	SpanID  SpanID
}

type SpanID struct {
	trace.SpanID
}

var _ json.Marshaler = SpanID{}

func (id SpanID) MarshalJSON() ([]byte, error) {
	if id.IsValid() {
		return id.SpanID.MarshalJSON()
	} else {
		return json.Marshal("")
	}
}

var _ json.Unmarshaler = (*SpanID)(nil)

func (id *SpanID) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return fmt.Errorf("unmarshal span ID: %w", err)
	}
	if str == "" {
		id.SpanID = trace.SpanID{}
		return nil
	}
	spanID, err := trace.SpanIDFromHex(str)
	if err != nil {
		return err
	}
	id.SpanID = spanID
	return nil
}

type TraceID struct {
	trace.TraceID
}

var _ json.Marshaler = TraceID{}

func (id TraceID) MarshalJSON() ([]byte, error) {
	if id.IsValid() {
		return id.TraceID.MarshalJSON()
	} else {
		return json.Marshal("")
	}
}

var _ json.Unmarshaler = (*TraceID)(nil)

func (id *TraceID) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return fmt.Errorf("unmarshal span ID: %w", err)
	}
	if str == "" {
		id.TraceID = trace.TraceID{}
		return nil
	}
	spanID, err := trace.TraceIDFromHex(str)
	if err != nil {
		return err
	}
	id.TraceID = spanID
	return nil
}
