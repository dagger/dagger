package protocol

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// EntryNumbers are engine entry numbers in an order both sides know, such as
// a restored file's records' (RestoredFile.Entries). On the wire they are
// one base64 string: each number as a zigzag varint of its difference from
// the number before it, the first from 0. A file's entries are made close
// together, so most take a byte or two, where a JSON array of numbers takes
// seven or eight.
type EntryNumbers []uint64

func (e EntryNumbers) MarshalJSON() ([]byte, error) {
	buf := make([]byte, 0, len(e)*2)
	var prev uint64
	for _, n := range e {
		buf = binary.AppendVarint(buf, int64(n-prev))
		prev = n
	}
	return json.Marshal(base64.StdEncoding.EncodeToString(buf))
}

func (e *EntryNumbers) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("entry numbers: %w", err)
	}
	buf, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return fmt.Errorf("entry numbers: %w", err)
	}
	numbers := EntryNumbers{}
	var prev uint64
	for len(buf) > 0 {
		delta, n := binary.Varint(buf)
		if n <= 0 {
			return errors.New("entry numbers: a number is cut off or too large")
		}
		prev += uint64(delta)
		numbers = append(numbers, prev)
		buf = buf[n:]
	}
	*e = numbers
	return nil
}
