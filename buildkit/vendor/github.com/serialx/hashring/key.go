package hashring

import (
	"encoding/binary"
	"fmt"
)

type Int64PairHashKey struct {
	High int64
	Low  int64
}

func (k *Int64PairHashKey) Less(other HashKey) bool {
	o := other.(*Int64PairHashKey)
	if k.High < o.High {
		return true
	}
	return k.High == o.High && k.Low < o.Low
}

func NewInt64PairHashKey(bytes []byte) (HashKey, error) {
	const expected = 16
	if len(bytes) != expected {
		return nil, fmt.Errorf(
			"expected %d bytes, got %d bytes",
			expected, len(bytes),
		)
	}
	return &Int64PairHashKey{
		High: int64(binary.LittleEndian.Uint64(bytes[:8])),
		Low:  int64(binary.LittleEndian.Uint64(bytes[8:])),
	}, nil
}
