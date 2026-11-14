package protocol

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dagger/dagger/dagql"
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

// PackCompleteParts packs a restored file's complete parts
// (RestoredFile.CompleteParts): for each position of the file's entries, the
// parts its entry has complete, none at most positions. It returns each
// distinct address once, in the order first named, and the positions that
// have any, packed as varints: the position's difference from the last such
// position, the number of its parts, then each part's index in the table.
func PackCompleteParts(parts [][]dagql.PersistedPartAddress) ([]dagql.PersistedPartAddress, []byte, error) {
	var table []dagql.PersistedPartAddress
	index := map[string]uint64{}
	var packed []byte
	last := 0
	for position, addresses := range parts {
		if len(addresses) == 0 {
			continue
		}
		packed = binary.AppendUvarint(packed, uint64(position-last))
		last = position
		packed = binary.AppendUvarint(packed, uint64(len(addresses)))
		for _, address := range addresses {
			key, err := json.Marshal(address)
			if err != nil {
				return nil, nil, err
			}
			i, ok := index[string(key)]
			if !ok {
				i = uint64(len(table))
				index[string(key)] = i
				table = append(table, address)
			}
			packed = binary.AppendUvarint(packed, i)
		}
	}
	return table, packed, nil
}

// UnpackCompleteParts returns, for each position of a restored file's
// entries that has complete parts, its parts' addresses, as
// PackCompleteParts packed them.
func UnpackCompleteParts(table []dagql.PersistedPartAddress, packed []byte) (map[int][]dagql.PersistedPartAddress, error) {
	parts := map[int][]dagql.PersistedPartAddress{}
	next := func() (uint64, error) {
		v, n := binary.Uvarint(packed)
		if n <= 0 {
			return 0, errors.New("complete parts: a number is cut off or too large")
		}
		packed = packed[n:]
		return v, nil
	}
	position := 0
	for len(packed) > 0 {
		delta, err := next()
		if err != nil {
			return nil, err
		}
		position += int(delta)
		count, err := next()
		if err != nil {
			return nil, err
		}
		addresses := make([]dagql.PersistedPartAddress, 0, count)
		for range count {
			i, err := next()
			if err != nil {
				return nil, err
			}
			if i >= uint64(len(table)) {
				return nil, fmt.Errorf("complete parts: part %d of a table of %d", i, len(table))
			}
			addresses = append(addresses, table[i])
		}
		parts[position] = addresses
	}
	return parts, nil
}
