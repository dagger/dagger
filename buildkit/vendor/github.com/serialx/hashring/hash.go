package hashring

import (
	"fmt"
	"hash"
)

// HashSum allows to use a builder pattern to create different HashFunc objects.
// See examples for details.
type HashSum struct {
	functions []func([]byte) []byte
}

func (r *HashSum) Use(
	hashKeyFunc func(bytes []byte) (HashKey, error),
) (HashFunc, error) {

	// build final hash function
	composed := func(bytes []byte) []byte {
		for _, f := range r.functions {
			bytes = f(bytes)
		}
		return bytes
	}

	// check function composition for errors
	testResult := composed([]byte("test"))
	_, err := hashKeyFunc(testResult)
	if err != nil {
		const msg = "can't use given hash.Hash with given hashKeyFunc"
		return nil, fmt.Errorf("%s: %w", msg, err)
	}

	// build HashFunc
	return func(key []byte) HashKey {
		bytes := composed(key)
		hashKey, err := hashKeyFunc(bytes)
		if err != nil {
			// panic because we already checked HashSum earlier
			panic(fmt.Sprintf("hashKeyFunc failure: %v", err))
		}
		return hashKey
	}, nil
}

// NewHash creates a new *HashSum object which can be used to create HashFunc.
// HashFunc object is thread safe if the hasher argument produces a new hash.Hash 
// each time. The produced hash.Hash is allowed to be non thread-safe.
func NewHash(hasher func() hash.Hash) *HashSum {
	return &HashSum{
		functions: []func(key []byte) []byte{
			func(key []byte) []byte {
				hash := hasher()
				hash.Write(key)
				return hash.Sum(nil)
			},
		},
	}
}

func (r *HashSum) FirstBytes(n int) *HashSum {
	r.functions = append(r.functions, func(bytes []byte) []byte {
		return bytes[:n]
	})
	return r
}

func (r *HashSum) LastBytes(n int) *HashSum {
	r.functions = append(r.functions, func(bytes []byte) []byte {
		return bytes[len(bytes)-n:]
	})
	return r
}
