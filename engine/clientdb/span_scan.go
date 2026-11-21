package clientdb

import (
	"context"
	"fmt"
)

// SpanSummary is a view of the fields of one span row that a search filters
// on, read without materializing the row. Its byte slices alias a read
// buffer: they are only valid during the ScanSpansLatest callback that
// received the summary, so a caller copies whatever it keeps.
type SpanSummary struct {
	// ID is the row's ID.
	ID           int64
	TraceID      []byte
	SpanID       []byte
	ParentSpanID []byte
	HasParent    bool
	Name         []byte
	StartTime    int64
	EndTime      int64
	Ended        bool
	StatusCode   int64
	Attributes   []byte
}

// ScanSpansLatest visits the newest snapshot row of every span in ids, in
// append order, as SelectSpansLatest returns them -- but one summary at a
// time, decoded in place: a search over a whole session reads the rows it
// filters without holding them, or even allocating them, all at once.
func (s *DB) ScanSpansLatest(ctx context.Context, ids map[string]struct{}, fn func(*SpanSummary) error) error {
	var sum SpanSummary
	err := s.spans.scanIDs(ctx, s.lookup.latestRowIDs(ids),
		func(frame []byte) error {
			if err := decodeSpanSummary(frame, &sum); err != nil {
				return fmt.Errorf("decode span row: %w", err)
			}
			return fn(&sum)
		},
		func(row Span) error {
			sum = SpanSummary{
				ID:           row.ID,
				TraceID:      []byte(row.TraceID),
				SpanID:       []byte(row.SpanID),
				ParentSpanID: []byte(row.ParentSpanID.String),
				HasParent:    row.ParentSpanID.Valid,
				Name:         []byte(row.Name),
				StartTime:    row.StartTime,
				EndTime:      row.EndTime.Int64,
				Ended:        row.EndTime.Valid,
				StatusCode:   row.StatusCode,
				Attributes:   row.Attributes,
			}
			return fn(&sum)
		})
	if err != nil {
		return fmt.Errorf("scan span rows: %w", err)
	}
	return nil
}

// decodeSpanSummary decodes the summary fields of an encoded span row (see
// encodeSpan) into out, aliasing buf rather than copying it.
func decodeSpanSummary(buf []byte, out *SpanSummary) error {
	d := rowDecoder{buf: buf}
	var err error
	if out.ID, err = d.int64(); err != nil {
		return err
	}
	if out.TraceID, err = d.stringView(); err != nil {
		return err
	}
	if out.SpanID, err = d.stringView(); err != nil {
		return err
	}
	if _, err = d.stringView(); err != nil { // TraceState
		return err
	}
	if out.HasParent, err = d.bool(); err != nil {
		return err
	}
	if out.ParentSpanID, err = d.stringView(); err != nil {
		return err
	}
	if _, err = d.int64(); err != nil { // Flags
		return err
	}
	if out.Name, err = d.stringView(); err != nil {
		return err
	}
	if _, err = d.stringView(); err != nil { // Kind
		return err
	}
	if out.StartTime, err = d.int64(); err != nil {
		return err
	}
	if out.Ended, err = d.bool(); err != nil {
		return err
	}
	if out.EndTime, err = d.int64(); err != nil {
		return err
	}
	if out.Attributes, err = d.bytesView(); err != nil {
		return err
	}
	if _, err = d.int64(); err != nil { // DroppedAttributesCount
		return err
	}
	if _, err = d.bytesView(); err != nil { // Events
		return err
	}
	if _, err = d.int64(); err != nil { // DroppedEventsCount
		return err
	}
	if _, err = d.bytesView(); err != nil { // Links
		return err
	}
	if _, err = d.int64(); err != nil { // DroppedLinksCount
		return err
	}
	if out.StatusCode, err = d.int64(); err != nil {
		return err
	}
	return nil
}

// stringView is string without the copy.
func (d *rowDecoder) stringView() ([]byte, error) {
	n, err := d.length()
	if err != nil {
		return nil, err
	}
	return d.take(n)
}

// bytesView is bytes without the copy.
func (d *rowDecoder) bytesView() ([]byte, error) {
	n, err := d.length()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	return d.take(n - 1)
}
