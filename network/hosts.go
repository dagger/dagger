package network

import (
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"strings"

	"github.com/opencontainers/go-digest"
	"github.com/zeebo/xxh3"
)

func HostHash(val digest.Digest) string {
	b, err := hex.DecodeString(val.Encoded())
	if err != nil {
		panic(err)
	}

	return strings.ToLower(b32(xxh3.Hash(b)))
}

func HostHashStr(val string) string {
	return strings.ToLower(b32(xxh3.HashString(val)))
}

// ClientDomain is a session-global domain suffix appended to every service's
// hostname. It is randomly generated on the first call.
//
// Ideally we would base this on the Buildkit gateway session ID instead of
// using global state, but for exporting we actually establish multiple gateway
// sessions.
func ClientDomain(sid string) string {
	return HostHashStr(sid) + DomainSuffix
}

func b32(n uint64) string {
	var sum [8]byte
	binary.BigEndian.PutUint64(sum[:], n)
	return base32.HexEncoding.
		WithPadding(base32.NoPadding).
		EncodeToString(sum[:])
}
