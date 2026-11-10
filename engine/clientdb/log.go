package clientdb

import (
	"fmt"
	"log/slog"
	"slices"
	"unicode/utf8"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// LogsToPB converts log rows to OTLP, grouped by resource and
// instrumentation scope, each group in the order its first row comes. Each
// distinct resource and scope is decoded once per call, as in SpansToPB.
func LogsToPB(dbLog []Log) []*otlplogsv1.ResourceLogs {
	if len(dbLog) == 0 {
		return nil
	}

	rsm := make(map[attribute.Distinct]*otlplogsv1.ResourceLogs)

	type key struct {
		r  attribute.Distinct
		is instrumentation.Scope
	}
	ssm := make(map[key]*otlplogsv1.ScopeLogs)

	type resourceKey struct {
		resource, schemaURL string
	}
	type decodedResource struct {
		res *sdkresource.Resource
		pb  *otlpresourcev1.Resource
		err error
	}
	resourceCache := map[resourceKey]decodedResource{}
	type decodedScope struct {
		scope instrumentation.Scope
		pb    *otlpcommonv1.InstrumentationScope
		err   error
	}
	scopeCache := map[string]decodedScope{}

	var rss []*otlplogsv1.ResourceLogs
	for _, sd := range dbLog {
		rk := resourceKey{string(sd.Resource), sd.ResourceSchemaURL}
		decodedRes, ok := resourceCache[rk]
		if !ok {
			decodedRes.pb = &otlpresourcev1.Resource{}
			if decodedRes.err = proto.Unmarshal(sd.Resource, decodedRes.pb); decodedRes.err == nil {
				decodedRes.res = telemetry.ResourceFromPB(sd.ResourceSchemaURL, decodedRes.pb)
			}
			resourceCache[rk] = decodedRes
		}
		if decodedRes.err != nil {
			slog.Error("failed to unmarshal log resource", "error", decodedRes.err, "log", sd)
			continue
		}
		res := decodedRes.res
		decodedSc, ok := scopeCache[string(sd.InstrumentationScope)]
		if !ok {
			decodedSc.pb = &otlpcommonv1.InstrumentationScope{}
			if decodedSc.err = proto.Unmarshal(sd.InstrumentationScope, decodedSc.pb); decodedSc.err == nil {
				decodedSc.scope = telemetry.InstrumentationScopeFromPB(decodedSc.pb)
			}
			scopeCache[string(sd.InstrumentationScope)] = decodedSc
		}
		if decodedSc.err != nil {
			slog.Error("failed to unmarshal instrumentation scope", "error", decodedSc.err, "log", sd)
			continue
		}
		scope := decodedSc.scope
		rKey := res.Equivalent()
		k := key{
			r:  rKey,
			is: scope,
		}
		scopeLog, iOk := ssm[k]
		if !iOk {
			// Either the resource or instrumentation scope were unknown.
			scopeLog = &otlplogsv1.ScopeLogs{
				Scope:      proto.CloneOf(decodedSc.pb),
				LogRecords: []*otlplogsv1.LogRecord{},
				SchemaUrl:  scope.SchemaURL,
			}
		}
		var bodyPb otlpcommonv1.AnyValue
		if err := proto.Unmarshal(sd.Body, &bodyPb); err != nil {
			slog.Warn("failed to unmarshal log body", "error", err, "log", sd)
			continue
		}
		var attrs []*otlpcommonv1.KeyValue
		if err := UnmarshalProtoList(sd.Attributes, &otlpcommonv1.KeyValue{}, &attrs); err != nil {
			slog.Warn("failed to unmarshal log attributes", "error", err)
			continue
		}
		if !sd.TraceID.Valid || !sd.SpanID.Valid {
			slog.Error("log record has invalid trace or span ID", "log", sd)
			continue
		}
		tid, err := trace.TraceIDFromHex(sd.TraceID.String)
		if err != nil {
			slog.Error("failed to unmarshal trace id", "error", err)
			continue
		}
		sid, err := trace.SpanIDFromHex(sd.SpanID.String)
		if err != nil {
			slog.Error("failed to unmarshal span id", "error", err)
			continue
		}
		scopeLog.LogRecords = append(scopeLog.LogRecords, &otlplogsv1.LogRecord{
			TimeUnixNano:   uint64(sd.Timestamp),
			SeverityNumber: otlplogsv1.SeverityNumber(sd.SeverityNumber),
			SeverityText:   sd.SeverityText,
			Body:           &bodyPb,
			Attributes:     attrs,
			TraceId:        tid[:],
			SpanId:         sid[:],
		})
		ssm[k] = scopeLog

		rs, rOk := rsm[rKey]
		if !rOk {
			// The resource was unknown.
			rs = &otlplogsv1.ResourceLogs{
				Resource:  decodedRes.pb,
				ScopeLogs: []*otlplogsv1.ScopeLogs{scopeLog},
				SchemaUrl: res.SchemaURL(),
			}
			rsm[rKey] = rs
			rss = append(rss, rs)
			continue
		}

		// The resource has been seen before. Check if the instrumentation
		// library lookup was unknown because if so we need to add it to the
		// ResourceSpans. Otherwise, the instrumentation library has already
		// been seen and the append we did above will be included it in the
		// ScopeSpans reference.
		if !iOk {
			rs.ScopeLogs = append(rs.ScopeLogs, scopeLog)
		}
	}
	return rss
}

// AppendLogsRequestPB appends the binary encoding of the OTLP logs export
// request carrying rows: byte for byte what proto.Marshal makes of
// ExportLogsServiceRequest{ResourceLogs: LogsToPB(rows)}, without building
// its messages. It drops the rows LogsToPB drops. A row's body and
// attributes were encoded with proto.Marshal from valid UTF-8 when it was
// stored, so they are written as stored: the body after its field's tag, and
// each element of the attribute list after its tag. Only a body or attribute
// damaged after it was stored, which LogsToPB would drop, is written as is. Resources and scopes are
// decoded and encoded again as LogsToPB makes them, each distinct one once
// per call. Every record's size is worked out first, so the request is
// written once, into room made for all of it.
func AppendLogsRequestPB(b []byte, rows []Log) ([]byte, error) {
	type scopeGroup struct {
		scope     []byte // the InstrumentationScope's encoding
		schemaURL string
		rows      []int // indexes into rows and records
		size      int   // the ScopeLogs' encoded size
	}
	type resourceGroup struct {
		resource  []byte // the Resource's encoding
		schemaURL string
		scopes    []*scopeGroup
		byScope   map[instrumentation.Scope]*scopeGroup
		size      int // the ResourceLogs' encoded size
	}
	type resourceKey struct {
		resource, schemaURL string
	}
	type decodedResource struct {
		key       attribute.Distinct
		schemaURL string
		pb        []byte
		err       error
	}
	type decodedScope struct {
		scope instrumentation.Scope
		pb    []byte
		err   error
	}
	resources := map[resourceKey]decodedResource{}
	scopes := map[string]decodedScope{}
	var groups []*resourceGroup
	byResource := map[attribute.Distinct]*resourceGroup{}
	records := make([]plannedLog, len(rows))
	for i := range rows {
		row := &rows[i]
		// The key is built in the lookup, so a hit doesn't copy the
		// resource's bytes into a string.
		res, ok := resources[resourceKey{string(row.Resource), row.ResourceSchemaURL}]
		if !ok {
			var pb otlpresourcev1.Resource
			if res.err = proto.Unmarshal(row.Resource, &pb); res.err == nil {
				r := telemetry.ResourceFromPB(row.ResourceSchemaURL, &pb)
				res.key, res.schemaURL = r.Equivalent(), r.SchemaURL()
				if res.pb, res.err = proto.Marshal(&pb); res.err != nil {
					return nil, fmt.Errorf("failed to proto marshal resource: %w", res.err)
				}
			}
			resources[resourceKey{string(row.Resource), row.ResourceSchemaURL}] = res
		}
		if res.err != nil {
			slog.Error("failed to unmarshal log resource", "error", res.err, "log", row)
			continue
		}
		sc, ok := scopes[string(row.InstrumentationScope)]
		if !ok {
			var pb otlpcommonv1.InstrumentationScope
			if sc.err = proto.Unmarshal(row.InstrumentationScope, &pb); sc.err == nil {
				sc.scope = telemetry.InstrumentationScopeFromPB(&pb)
				if sc.pb, sc.err = proto.Marshal(&pb); sc.err != nil {
					return nil, fmt.Errorf("failed to proto marshal instrumentation scope: %w", sc.err)
				}
			}
			scopes[string(row.InstrumentationScope)] = sc
		}
		if sc.err != nil {
			slog.Error("failed to unmarshal instrumentation scope", "error", sc.err, "log", row)
			continue
		}
		if ok, err := records[i].plan(row); err != nil {
			return nil, err
		} else if !ok {
			continue
		}

		rg := byResource[res.key]
		if rg == nil {
			// Like proto.Marshal, fail on a schema URL that isn't valid
			// UTF-8. The resource and scope were checked when they were
			// decoded.
			if !utf8.ValidString(res.schemaURL) {
				return nil, errInvalidUTF8
			}
			rg = &resourceGroup{resource: res.pb, schemaURL: res.schemaURL, byScope: map[instrumentation.Scope]*scopeGroup{}}
			byResource[res.key] = rg
			groups = append(groups, rg)
		}
		sg := rg.byScope[sc.scope]
		if sg == nil {
			sg = &scopeGroup{scope: sc.pb, schemaURL: sc.scope.SchemaURL}
			rg.byScope[sc.scope] = sg
			rg.scopes = append(rg.scopes, sg)
		}
		sg.rows = append(sg.rows, i)
		sg.size += protowire.SizeTag(2) + protowire.SizeBytes(records[i].size)
	}

	total := 0
	for _, rg := range groups {
		rg.size = protowire.SizeTag(1) + protowire.SizeBytes(len(rg.resource))
		for _, sg := range rg.scopes {
			sg.size += protowire.SizeTag(1) + protowire.SizeBytes(len(sg.scope))
			if sg.schemaURL != "" {
				sg.size += protowire.SizeTag(3) + protowire.SizeBytes(len(sg.schemaURL))
			}
			rg.size += protowire.SizeTag(2) + protowire.SizeBytes(sg.size)
		}
		if rg.schemaURL != "" {
			rg.size += protowire.SizeTag(3) + protowire.SizeBytes(len(rg.schemaURL))
		}
		total += protowire.SizeTag(1) + protowire.SizeBytes(rg.size)
	}
	b = slices.Grow(b, total)
	for _, rg := range groups {
		// ExportLogsServiceRequest's field 1: one ResourceLogs.
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendVarint(b, uint64(rg.size))
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendBytes(b, rg.resource)
		for _, sg := range rg.scopes {
			b = protowire.AppendTag(b, 2, protowire.BytesType)
			b = protowire.AppendVarint(b, uint64(sg.size))
			b = protowire.AppendTag(b, 1, protowire.BytesType)
			b = protowire.AppendBytes(b, sg.scope)
			for _, i := range sg.rows {
				b = protowire.AppendTag(b, 2, protowire.BytesType)
				b = protowire.AppendVarint(b, uint64(records[i].size))
				b = records[i].append(b, &rows[i])
			}
			if sg.schemaURL != "" {
				b = protowire.AppendTag(b, 3, protowire.BytesType)
				b = protowire.AppendString(b, sg.schemaURL)
			}
		}
		if rg.schemaURL != "" {
			b = protowire.AppendTag(b, 3, protowire.BytesType)
			b = protowire.AppendString(b, rg.schemaURL)
		}
	}
	return b, nil
}

// plannedLog is the OTLP log record LogsToPB makes of a row, worked out
// ahead of its encoding: the IDs parsed from the row and the record's
// encoded size.
type plannedLog struct {
	tid      trace.TraceID
	sid      trace.SpanID
	severity otlplogsv1.SeverityNumber
	size     int
}

// plan works out row's OTLP log record. It reports false for a row LogsToPB
// drops: one without valid trace and span IDs, or whose attribute list can't
// be split into elements. Like proto.Marshal, it fails on a severity text
// that isn't valid UTF-8.
func (pl *plannedLog) plan(row *Log) (bool, error) {
	if storedListSize(6, row.Attributes) == 0 && len(row.Attributes) > 0 {
		slog.Warn("failed to unmarshal log attributes", "error", "failed to decode list element")
		return false, nil
	}
	if !row.TraceID.Valid || !row.SpanID.Valid {
		slog.Error("log record has invalid trace or span ID", "log", row)
		return false, nil
	}
	tid, err := trace.TraceIDFromHex(row.TraceID.String)
	if err != nil {
		slog.Error("failed to unmarshal trace id", "error", err)
		return false, nil
	}
	sid, err := trace.SpanIDFromHex(row.SpanID.String)
	if err != nil {
		slog.Error("failed to unmarshal span id", "error", err)
		return false, nil
	}
	if !utf8.ValidString(row.SeverityText) {
		return false, errInvalidUTF8
	}
	*pl = plannedLog{tid: tid, sid: sid, severity: otlplogsv1.SeverityNumber(row.SeverityNumber)}
	n := 0
	if row.Timestamp != 0 {
		n += protowire.SizeTag(1) + protowire.SizeFixed64()
	}
	if pl.severity != 0 {
		n += protowire.SizeTag(2) + protowire.SizeVarint(uint64(pl.severity))
	}
	if row.SeverityText != "" {
		n += protowire.SizeTag(3) + protowire.SizeBytes(len(row.SeverityText))
	}
	// LogsToPB always sets a body, empty for a row stored without one.
	n += protowire.SizeTag(5) + protowire.SizeBytes(len(row.Body))
	n += storedListSize(6, row.Attributes)
	n += protowire.SizeTag(9) + protowire.SizeBytes(len(tid))
	n += protowire.SizeTag(10) + protowire.SizeBytes(len(sid))
	pl.size = n
	return true, nil
}

// append appends row's OTLP log record as planned, in field order, as
// proto.Marshal does.
func (pl *plannedLog) append(b []byte, row *Log) []byte {
	if row.Timestamp != 0 {
		b = protowire.AppendTag(b, 1, protowire.Fixed64Type)
		b = protowire.AppendFixed64(b, uint64(row.Timestamp))
	}
	if pl.severity != 0 {
		b = protowire.AppendTag(b, 2, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(pl.severity))
	}
	if row.SeverityText != "" {
		b = protowire.AppendTag(b, 3, protowire.BytesType)
		b = protowire.AppendString(b, row.SeverityText)
	}
	b = protowire.AppendTag(b, 5, protowire.BytesType)
	b = protowire.AppendBytes(b, row.Body)
	b = appendStoredList(b, 6, row.Attributes, "log attributes")
	b = protowire.AppendTag(b, 9, protowire.BytesType)
	b = protowire.AppendBytes(b, pl.tid[:])
	b = protowire.AppendTag(b, 10, protowire.BytesType)
	b = protowire.AppendBytes(b, pl.sid[:])
	return b
}
