package network

import (
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql/call"
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

// SessionDomain is a session-wide domain suffix for a given session ID.
func SessionDomain(sid string) string {
	return HostHashStr(sid) + DomainSuffix
}

// SessionDomain is a session-wide domain suffix for a given session ID.
func ModuleDomain(modID *call.ID, sid string) string {
	return fmt.Sprintf(
		"%s.%s%s",
		HostHash(modID.Digest()),
		HostHashStr(sid),
		DomainSuffix,
	)
}

func b32(n uint64) string {
	var sum [8]byte
	binary.BigEndian.PutUint64(sum[:], n)
	return base32.HexEncoding.
		WithPadding(base32.NoPadding).
		EncodeToString(sum[:])
}
