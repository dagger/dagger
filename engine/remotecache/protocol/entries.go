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
func PackCompleteParts(parts [][]dagql.PersistedPartAddress) ([]dagql.PersistedPartAddress, []byte) {
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
			// Go syntax quotes every string, so two addresses share a key
			// only when they are equal.
			key := fmt.Sprintf("%#v", address)
			i, ok := index[key]
			if !ok {
				i = uint64(len(table))
				index[key] = i
				table = append(table, address)
			}
			packed = binary.AppendUvarint(packed, i)
		}
	}
	return table, packed
}

// UnpackCompleteParts returns, for each position of a restored file's
// entries that has complete parts, its parts' addresses, as
// PackCompleteParts packed them.
func UnpackCompleteParts(table []dagql.PersistedPartAddress, packed []byte) (map[int][]dagql.PersistedPartAddress, error) {
	parts := map[int][]dagql.PersistedPartAddress{}
	err := readCompleteParts(packed, len(table), func(position int, indexes []uint64) error {
		addresses := make([]dagql.PersistedPartAddress, 0, len(indexes))
		for _, i := range indexes {
			addresses = append(addresses, table[i])
		}
		parts[position] = addresses
		return nil
	})
	if err != nil {
		return nil, err
	}
	return parts, nil
}

// PartIndexes are a restored file's complete parts (PackCompleteParts) for
// a reader that keeps them by position: each part as its index in the
// table, four bytes, where an unpacked address takes forty and a slice of
// them more for each position.
type PartIndexes struct {
	// position p's parts are indexes[off[p]:off[p+1]]; off is nil when no
	// position has any.
	off     []uint32
	indexes []uint32
}

// UnpackPartIndexes reads packed complete parts of a file's n entries, with
// a table of tableLen addresses, by position (PartIndexes). It refuses what
// UnpackCompleteParts refuses, a position at or past n, and positions out of
// order.
func UnpackPartIndexes(packed []byte, tableLen, n int) (PartIndexes, error) {
	if len(packed) == 0 {
		return PartIndexes{}, nil
	}
	count := 0
	if err := readCompleteParts(packed, tableLen, func(position int, indexes []uint64) error {
		if position >= n {
			return fmt.Errorf("complete parts at position %d of %d entries", position, n)
		}
		count += len(indexes)
		return nil
	}); err != nil {
		return PartIndexes{}, err
	}
	x := PartIndexes{off: make([]uint32, n+1), indexes: make([]uint32, 0, count)}
	// off[p] is set for every p up to done.
	done := 0
	err := readCompleteParts(packed, tableLen, func(position int, indexes []uint64) error {
		if position < done {
			return fmt.Errorf("complete parts at position %d after position %d", position, done-1)
		}
		for ; done < position; done++ {
			x.off[done+1] = uint32(len(x.indexes))
		}
		for _, i := range indexes {
			x.indexes = append(x.indexes, uint32(i))
		}
		done++
		x.off[done] = uint32(len(x.indexes))
		return nil
	})
	if err != nil {
		return PartIndexes{}, err
	}
	for ; done < n; done++ {
		x.off[done+1] = uint32(len(x.indexes))
	}
	return x, nil
}

// At returns the table indexes of position p's parts.
func (x PartIndexes) At(p int) []uint32 {
	if x.off == nil || p < 0 || p+1 >= len(x.off) {
		return nil
	}
	return x.indexes[x.off[p]:x.off[p+1]]
}

// readCompleteParts calls visit with each position of packed complete parts
// (PackCompleteParts) that has any, in order, and its parts' indexes in the
// table, which visit must not keep. It refuses a list that ends mid-record,
// or names an index at or past tableLen.
func readCompleteParts(packed []byte, tableLen int, visit func(position int, indexes []uint64) error) error {
	next := func() (uint64, error) {
		v, n := binary.Uvarint(packed)
		if n <= 0 {
			return 0, errors.New("complete parts: a number is cut off or too large")
		}
		packed = packed[n:]
		return v, nil
	}
	var indexes []uint64
	position := 0
	for len(packed) > 0 {
		delta, err := next()
		if err != nil {
			return err
		}
		position += int(delta)
		count, err := next()
		if err != nil {
			return err
		}
		if count > uint64(len(packed)) {
			// Each index takes at least a byte.
			return fmt.Errorf("complete parts: %d parts announced, with %d bytes left", count, len(packed))
		}
		indexes = indexes[:0]
		for range count {
			i, err := next()
			if err != nil {
				return err
			}
			if i >= uint64(tableLen) {
				return fmt.Errorf("complete parts: part %d of a table of %d", i, tableLen)
			}
			indexes = append(indexes, i)
		}
		if err := visit(position, indexes); err != nil {
			return err
		}
	}
	return nil
}
