package clientdb

import (
	"database/sql"
	"encoding/binary"
	"fmt"
)

type rowCodec[Row any] struct {
	encode func(Row) []byte
	decode func([]byte) (Row, error)
	size   func(Row) int64
	getID  func(Row) int64
	setID  func(*Row, int64)
}

type rowEncoder struct {
	buf []byte
}

func (e *rowEncoder) int64(v int64) {
	e.buf = binary.AppendVarint(e.buf, v)
}

func (e *rowEncoder) bool(v bool) {
	if v {
		e.buf = append(e.buf, 1)
	} else {
		e.buf = append(e.buf, 0)
	}
}

func (e *rowEncoder) string(v string) {
	e.buf = binary.AppendUvarint(e.buf, uint64(len(v)))
	e.buf = append(e.buf, v...)
}

// bytes reserves zero for nil so empty and nil blobs survive a round trip.
func (e *rowEncoder) bytes(v []byte) {
	if v == nil {
		e.buf = binary.AppendUvarint(e.buf, 0)
		return
	}
	e.buf = binary.AppendUvarint(e.buf, uint64(len(v))+1)
	e.buf = append(e.buf, v...)
}

// Unlike SQLite's NULL round trip, the codec preserves String when Valid is
// false; consumers must guard Valid before reading String.
func (e *rowEncoder) nullString(v sql.NullString) {
	e.bool(v.Valid)
	e.string(v.String)
}

func (e *rowEncoder) nullInt64(v sql.NullInt64) {
	e.bool(v.Valid)
	e.int64(v.Int64)
}

type rowDecoder struct {
	buf []byte
	off int
}

func (d *rowDecoder) int64() (int64, error) {
	v, n := binary.Varint(d.buf[d.off:])
	if n == 0 {
		return 0, fmt.Errorf("decode int64: truncated varint")
	}
	if n < 0 {
		return 0, fmt.Errorf("decode int64: varint overflow")
	}
	d.off += n
	return v, nil
}

func (d *rowDecoder) bool() (bool, error) {
	if d.off == len(d.buf) {
		return false, fmt.Errorf("decode bool: unexpected end of row")
	}
	v := d.buf[d.off]
	d.off++
	switch v {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("decode bool: invalid value %d", v)
	}
}

func (d *rowDecoder) length() (int, error) {
	v, n := binary.Uvarint(d.buf[d.off:])
	if n == 0 {
		return 0, fmt.Errorf("decode length: truncated varint")
	}
	if n < 0 {
		return 0, fmt.Errorf("decode length: varint overflow")
	}
	d.off += n
	if v > uint64(maxInt) {
		return 0, fmt.Errorf("decode length: %d overflows int", v)
	}
	return int(v), nil
}

func (d *rowDecoder) take(n int) ([]byte, error) {
	if n < 0 || n > len(d.buf)-d.off {
		return nil, fmt.Errorf("decode field of length %d: only %d bytes remain", n, len(d.buf)-d.off)
	}
	v := d.buf[d.off : d.off+n]
	d.off += n
	return v, nil
}

func (d *rowDecoder) string() (string, error) {
	n, err := d.length()
	if err != nil {
		return "", err
	}
	v, err := d.take(n)
	if err != nil {
		return "", err
	}
	return string(v), nil
}

func (d *rowDecoder) bytes() ([]byte, error) {
	n, err := d.length()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	n--
	v, err := d.take(n)
	if err != nil {
		return nil, err
	}
	out := make([]byte, n)
	copy(out, v)
	return out, nil
}

func (d *rowDecoder) nullString() (sql.NullString, error) {
	valid, err := d.bool()
	if err != nil {
		return sql.NullString{}, err
	}
	v, err := d.string()
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: v, Valid: valid}, nil
}

func (d *rowDecoder) nullInt64() (sql.NullInt64, error) {
	valid, err := d.bool()
	if err != nil {
		return sql.NullInt64{}, err
	}
	v, err := d.int64()
	if err != nil {
		return sql.NullInt64{}, err
	}
	return sql.NullInt64{Int64: v, Valid: valid}, nil
}

func (d *rowDecoder) done() error {
	if d.off != len(d.buf) {
		return fmt.Errorf("decode row: %d trailing bytes", len(d.buf)-d.off)
	}
	return nil
}

var spanCodec = rowCodec[Span]{
	encode: encodeSpan,
	decode: decodeSpan,
	size:   sizeSpan,
	getID:  func(row Span) int64 { return row.ID },
	setID:  func(row *Span, id int64) { row.ID = id },
}

func sizeSpan(row Span) int64 {
	return 8*11 + int64(len(row.TraceID)+len(row.SpanID)+len(row.TraceState)+len(row.ParentSpanID.String)+
		len(row.Name)+len(row.Kind)+len(row.Attributes)+len(row.Events)+len(row.Links)+
		len(row.StatusMessage)+len(row.InstrumentationScope)+len(row.Resource)+len(row.ResourceSchemaUrl))
}

func encodeSpan(row Span) []byte {
	e := rowEncoder{buf: make([]byte, 0, sizeSpan(row))}
	e.int64(row.ID)
	e.string(row.TraceID)
	e.string(row.SpanID)
	e.string(row.TraceState)
	e.nullString(row.ParentSpanID)
	e.int64(row.Flags)
	e.string(row.Name)
	e.string(row.Kind)
	e.int64(row.StartTime)
	e.nullInt64(row.EndTime)
	e.bytes(row.Attributes)
	e.int64(row.DroppedAttributesCount)
	e.bytes(row.Events)
	e.int64(row.DroppedEventsCount)
	e.bytes(row.Links)
	e.int64(row.DroppedLinksCount)
	e.int64(row.StatusCode)
	e.string(row.StatusMessage)
	e.bytes(row.InstrumentationScope)
	e.bytes(row.Resource)
	e.string(row.ResourceSchemaUrl)
	return e.buf
}

func decodeSpan(buf []byte) (Span, error) {
	d := rowDecoder{buf: buf}
	var row Span
	var err error
	if row.ID, err = d.int64(); err != nil {
		return Span{}, err
	}
	if row.TraceID, err = d.string(); err != nil {
		return Span{}, err
	}
	if row.SpanID, err = d.string(); err != nil {
		return Span{}, err
	}
	if row.TraceState, err = d.string(); err != nil {
		return Span{}, err
	}
	if row.ParentSpanID, err = d.nullString(); err != nil {
		return Span{}, err
	}
	if row.Flags, err = d.int64(); err != nil {
		return Span{}, err
	}
	if row.Name, err = d.string(); err != nil {
		return Span{}, err
	}
	if row.Kind, err = d.string(); err != nil {
		return Span{}, err
	}
	if row.StartTime, err = d.int64(); err != nil {
		return Span{}, err
	}
	if row.EndTime, err = d.nullInt64(); err != nil {
		return Span{}, err
	}
	if row.Attributes, err = d.bytes(); err != nil {
		return Span{}, err
	}
	if row.DroppedAttributesCount, err = d.int64(); err != nil {
		return Span{}, err
	}
	if row.Events, err = d.bytes(); err != nil {
		return Span{}, err
	}
	if row.DroppedEventsCount, err = d.int64(); err != nil {
		return Span{}, err
	}
	if row.Links, err = d.bytes(); err != nil {
		return Span{}, err
	}
	if row.DroppedLinksCount, err = d.int64(); err != nil {
		return Span{}, err
	}
	if row.StatusCode, err = d.int64(); err != nil {
		return Span{}, err
	}
	if row.StatusMessage, err = d.string(); err != nil {
		return Span{}, err
	}
	if row.InstrumentationScope, err = d.bytes(); err != nil {
		return Span{}, err
	}
	if row.Resource, err = d.bytes(); err != nil {
		return Span{}, err
	}
	if row.ResourceSchemaUrl, err = d.string(); err != nil {
		return Span{}, err
	}
	return row, d.done()
}

var logCodec = rowCodec[Log]{
	encode: encodeLog,
	decode: decodeLog,
	size:   sizeLog,
	getID:  func(row Log) int64 { return row.ID },
	setID:  func(row *Log, id int64) { row.ID = id },
}

func sizeLog(row Log) int64 {
	return 8*4 + int64(len(row.TraceID.String)+len(row.SpanID.String)+len(row.SeverityText)+
		len(row.Body)+len(row.Attributes)+len(row.InstrumentationScope)+len(row.Resource)+len(row.ResourceSchemaUrl))
}

func encodeLog(row Log) []byte {
	e := rowEncoder{buf: make([]byte, 0, sizeLog(row))}
	e.int64(row.ID)
	e.nullString(row.TraceID)
	e.nullString(row.SpanID)
	e.int64(row.Timestamp)
	e.int64(row.SeverityNumber)
	e.string(row.SeverityText)
	e.bytes(row.Body)
	e.bytes(row.Attributes)
	e.bytes(row.InstrumentationScope)
	e.bytes(row.Resource)
	e.string(row.ResourceSchemaUrl)
	return e.buf
}

func decodeLog(buf []byte) (Log, error) {
	d := rowDecoder{buf: buf}
	var row Log
	var err error
	if row.ID, err = d.int64(); err != nil {
		return Log{}, err
	}
	if row.TraceID, err = d.nullString(); err != nil {
		return Log{}, err
	}
	if row.SpanID, err = d.nullString(); err != nil {
		return Log{}, err
	}
	if row.Timestamp, err = d.int64(); err != nil {
		return Log{}, err
	}
	if row.SeverityNumber, err = d.int64(); err != nil {
		return Log{}, err
	}
	if row.SeverityText, err = d.string(); err != nil {
		return Log{}, err
	}
	if row.Body, err = d.bytes(); err != nil {
		return Log{}, err
	}
	if row.Attributes, err = d.bytes(); err != nil {
		return Log{}, err
	}
	if row.InstrumentationScope, err = d.bytes(); err != nil {
		return Log{}, err
	}
	if row.Resource, err = d.bytes(); err != nil {
		return Log{}, err
	}
	if row.ResourceSchemaUrl, err = d.string(); err != nil {
		return Log{}, err
	}
	return row, d.done()
}

var metricCodec = rowCodec[Metric]{
	encode: func(row Metric) []byte {
		e := rowEncoder{buf: make([]byte, 0, sizeMetric(row))}
		e.int64(row.ID)
		e.bytes(row.Data)
		return e.buf
	},
	decode: func(buf []byte) (Metric, error) {
		d := rowDecoder{buf: buf}
		id, err := d.int64()
		if err != nil {
			return Metric{}, err
		}
		data, err := d.bytes()
		if err != nil {
			return Metric{}, err
		}
		return Metric{ID: id, Data: data}, d.done()
	},
	size:  sizeMetric,
	getID: func(row Metric) int64 { return row.ID },
	setID: func(row *Metric, id int64) { row.ID = id },
}

func sizeMetric(row Metric) int64 {
	return 8 + int64(len(row.Data))
}

const maxInt = int(^uint(0) >> 1)
