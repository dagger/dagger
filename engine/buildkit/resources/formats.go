package resources

import (
	"bytes"
	"iter"
	"strconv"
	"strings"
)

// utils for parsing cgroup+pressure files
// cgroup docs: https://docs.kernel.org/admin-guide/cgroup-v2.html#format
// pressure docs: https://docs.kernel.org/accounting/psi.html#pressure-interface

type pressure struct {
	// averages are reported in cgroup file as decimal percentages (e.g. 0.00)
	// but we convert them to int64 by multiplying by 100 to avoid float plumbing
	// and float arithmetic nonsense
	//
	// total is in units of microseconds

	someAvg10  int64
	someAvg60  int64
	someAvg300 int64
	someTotal  int64
	fullAvg10  int64
	fullAvg60  int64
	fullAvg300 int64
	fullTotal  int64
}

func parsePressure(bs []byte) *pressure {
	var p pressure
	for key, values := range nestedKeyValuesStr(bs) {
		switch key {
		case "some":
			for key, value := range values {
				switch key {
				case "avg10":
					v, err := strconv.ParseFloat(value, 64)
					if err == nil {
						p.someAvg10 = int64(v * 100)
					}
				case "avg60":
					v, err := strconv.ParseFloat(value, 64)
					if err == nil {
						p.someAvg60 = int64(v * 100)
					}
				case "avg300":
					v, err := strconv.ParseFloat(value, 64)
					if err == nil {
						p.someAvg300 = int64(v * 100)
					}
				case "total":
					p.someTotal, _ = strconv.ParseInt(value, 10, 64)
				}
			}
		case "full":
			for key, value := range values {
				switch key {
				case "avg10":
					v, err := strconv.ParseFloat(value, 64)
					if err == nil {
						p.fullAvg10 = int64(v * 100)
					}
				case "avg60":
					v, err := strconv.ParseFloat(value, 64)
					if err == nil {
						p.fullAvg60 = int64(v * 100)
					}
				case "avg300":
					v, err := strconv.ParseFloat(value, 64)
					if err == nil {
						p.fullAvg300 = int64(v * 100)
					}
				case "total":
					p.fullTotal, _ = strconv.ParseInt(value, 10, 64)
				}
			}
		}
	}
	return &p
}

func nestedKeyValuesStr(bs []byte) iter.Seq2[string, iter.Seq2[string, string]] {
	return func(yield func(string, iter.Seq2[string, string]) bool) {
		for line := range lines(bs) {
			fields := bytes.Fields(line)
			if len(fields) < 2 {
				continue
			}
			key := string(fields[0])
			if !yield(key, func(yield func(string, string) bool) {
				for _, field := range fields[1:] {
					subkey, subvalue, ok := bytes.Cut(field, []byte("="))
					if !ok {
						continue
					}
					if !yield(string(subkey), string(subvalue)) {
						return
					}
				}
			}) {
				return
			}
		}
	}
}

func nestedKeyValuesInt64(bs []byte) iter.Seq2[string, iter.Seq2[string, int64]] {
	return func(yield func(string, iter.Seq2[string, int64]) bool) {
		for key, subkeyValues := range nestedKeyValuesStr(bs) {
			if !yield(key, func(yield func(string, int64) bool) {
				for subkey, subvalueStr := range subkeyValues {
					subvalue, err := strconv.ParseInt(subvalueStr, 10, 64)
					if err == nil && !yield(subkey, subvalue) {
						return
					}
				}
			}) {
				return
			}
		}
	}
}

func flatKeyValuesInt64(bs []byte) iter.Seq2[string, int64] {
	return func(yield func(string, int64) bool) {
		for line := range lines(bs) {
			fields := bytes.Fields(line)
			if len(fields) < 2 {
				continue
			}
			for _, field := range fields[1:] {
				value, err := strconv.ParseInt(string(field), 10, 64)
				if err == nil && !yield(string(fields[0]), value) {
					return
				}
			}
		}
	}
}

func lines(bs []byte) func(yield func([]byte) bool) {
	return func(yield func([]byte) bool) {
		for _, line := range bytes.Split(bs, []byte("\n")) {
			if !yield(line) {
				return
			}
		}
	}
}

func singleValue(bs []byte) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(string(bs)), 10, 64)
}
