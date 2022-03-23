package json

import (
	"strconv"
	"time"
)

const (
	// Import from zerolog/global.go
	timeFormatUnix   = ""
	timeFormatUnixMs = "UNIXMS"
	timeFormatUnixMicro = "UNIXMICRO"
)

// AppendTime formats the input time with the given format
// and appends the encoded string to the input byte slice.
func (e Encoder) AppendTime(dst []byte, t time.Time, format string) []byte {
	switch format {
	case timeFormatUnix:
		return e.AppendInt64(dst, t.Unix())
	case timeFormatUnixMs:
		return e.AppendInt64(dst, t.UnixNano()/1000000)
	case timeFormatUnixMicro:
		return e.AppendInt64(dst, t.UnixNano()/1000)
	}
	return append(t.AppendFormat(append(dst, '"'), format), '"')
}

// AppendTimes converts the input times with the given format
// and appends the encoded string list to the input byte slice.
func (Encoder) AppendTimes(dst []byte, vals []time.Time, format string) []byte {
	switch format {
	case timeFormatUnix:
		return appendUnixTimes(dst, vals)
	case timeFormatUnixMs:
		return appendUnixMsTimes(dst, vals)
	}
	if len(vals) == 0 {
		return append(dst, '[', ']')
	}
	dst = append(dst, '[')
	dst = append(vals[0].AppendFormat(append(dst, '"'), format), '"')
	if len(vals) > 1 {
		for _, t := range vals[1:] {
			dst = append(t.AppendFormat(append(dst, ',', '"'), format), '"')
		}
	}
	dst = append(dst, ']')
	return dst
}

func appendUnixTimes(dst []byte, vals []time.Time) []byte {
	if len(vals) == 0 {
		return append(dst, '[', ']')
	}
	dst = append(dst, '[')
	dst = strconv.AppendInt(dst, vals[0].Unix(), 10)
	if len(vals) > 1 {
		for _, t := range vals[1:] {
			dst = strconv.AppendInt(append(dst, ','), t.Unix(), 10)
		}
	}
	dst = append(dst, ']')
	return dst
}

func appendUnixMsTimes(dst []byte, vals []time.Time) []byte {
	if len(vals) == 0 {
		return append(dst, '[', ']')
	}
	dst = append(dst, '[')
	dst = strconv.AppendInt(dst, vals[0].UnixNano()/1000000, 10)
	if len(vals) > 1 {
		for _, t := range vals[1:] {
			dst = strconv.AppendInt(append(dst, ','), t.UnixNano()/1000000, 10)
		}
	}
	dst = append(dst, ']')
	return dst
}

// AppendDuration formats the input duration with the given unit & format
// and appends the encoded string to the input byte slice.
func (e Encoder) AppendDuration(dst []byte, d time.Duration, unit time.Duration, useInt bool) []byte {
	if useInt {
		return strconv.AppendInt(dst, int64(d/unit), 10)
	}
	return e.AppendFloat64(dst, float64(d)/float64(unit))
}

// AppendDurations formats the input durations with the given unit & format
// and appends the encoded string list to the input byte slice.
func (e Encoder) AppendDurations(dst []byte, vals []time.Duration, unit time.Duration, useInt bool) []byte {
	if len(vals) == 0 {
		return append(dst, '[', ']')
	}
	dst = append(dst, '[')
	dst = e.AppendDuration(dst, vals[0], unit, useInt)
	if len(vals) > 1 {
		for _, d := range vals[1:] {
			dst = e.AppendDuration(append(dst, ','), d, unit, useInt)
		}
	}
	dst = append(dst, ']')
	return dst
}
