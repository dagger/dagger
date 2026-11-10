// Package pack is the format of a remote-cache pack: one stored object that
// holds several small layer blobs, so that they upload, download and are
// addressed as one. A pack is a header listing its members, then the
// members' bytes in that order, unchanged. Its ID is the digest of its
// header, so the service names a pack from its member list alone, and an
// engine verifies the header against the ID and each member against its own
// digest, as it verifies any blob.
package pack

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/opencontainers/go-digest"
)

const (
	// magic is the header's first line.
	magic = "dagger-pack v1\n"

	// MaxMembers, MaxHeaderBytes and MaxBytes bound a pack an engine reads:
	// its members, its header and its whole object.
	MaxMembers     = 4096
	MaxHeaderBytes = 512 << 10
	MaxBytes       = 64 << 20
)

// Member is one blob of a pack, in pack order.
type Member struct {
	Digest digest.Digest `json:"digest"`
	Size   int64         `json:"size"`
}

// Header returns the header of a pack of members:
//
//	dagger-pack v1
//	<n>
//	sha256:<hex> <size>   (one line per member, in order)
//
// It fails on no members, a digest that is not a valid sha256, a negative
// size, a member listed twice, or a pack over the limits.
func Header(members []Member) ([]byte, error) {
	if len(members) == 0 {
		return nil, errors.New("pack has no members")
	}
	if len(members) > MaxMembers {
		return nil, fmt.Errorf("pack has %d members, over %d", len(members), MaxMembers)
	}
	var b bytes.Buffer
	b.WriteString(magic)
	b.WriteString(strconv.Itoa(len(members)))
	b.WriteByte('\n')
	seen := make(map[digest.Digest]bool, len(members))
	var total int64
	for _, m := range members {
		if err := checkMember(m); err != nil {
			return nil, err
		}
		if seen[m.Digest] {
			return nil, fmt.Errorf("pack lists %s twice", m.Digest)
		}
		seen[m.Digest] = true
		if m.Size > MaxBytes-total {
			return nil, fmt.Errorf("pack is over %d bytes", MaxBytes)
		}
		total += m.Size
		b.WriteString(m.Digest.String())
		b.WriteByte(' ')
		b.WriteString(strconv.FormatInt(m.Size, 10))
		b.WriteByte('\n')
	}
	if b.Len() > MaxHeaderBytes {
		return nil, fmt.Errorf("pack header is %d bytes, over %d", b.Len(), MaxHeaderBytes)
	}
	if total > MaxBytes-int64(b.Len()) {
		return nil, fmt.Errorf("pack is over %d bytes", MaxBytes)
	}
	return b.Bytes(), nil
}

func checkMember(m Member) error {
	if m.Digest.Algorithm() != digest.SHA256 {
		return fmt.Errorf("pack member %q is not sha256", m.Digest)
	}
	if err := m.Digest.Validate(); err != nil {
		return fmt.Errorf("pack member: %w", err)
	}
	if m.Size < 0 {
		return fmt.Errorf("pack member %s has negative size", m.Digest)
	}
	return nil
}

// ID returns the ID of a pack of members: the sha256 digest of its header.
func ID(members []Member) (digest.Digest, error) {
	header, err := Header(members)
	if err != nil {
		return "", err
	}
	return digest.SHA256.FromBytes(header), nil
}

// Size returns the length of a pack of members: its header and every
// member.
func Size(members []Member) (int64, error) {
	header, err := Header(members)
	if err != nil {
		return 0, err
	}
	size := int64(len(header))
	for _, m := range members {
		size += m.Size
	}
	return size, nil
}

// ReadHeader reads a pack's header from r, leaving r at the first member's
// bytes, and checks it against the pack's ID: the bytes read must be the
// header Header writes for the members they list, and hash to the ID. It
// returns the members.
func ReadHeader(r *bufio.Reader, id digest.Digest) ([]Member, error) {
	if err := id.Validate(); err != nil || id.Algorithm() != digest.SHA256 {
		return nil, fmt.Errorf("invalid pack ID %q", id)
	}
	var raw bytes.Buffer
	read := 0
	line := func() (string, error) {
		s, err := r.ReadSlice('\n')
		raw.Write(s)
		read += len(s)
		switch {
		case errors.Is(err, bufio.ErrBufferFull), err == nil && read > MaxHeaderBytes:
			return "", fmt.Errorf("pack %s header is over %d bytes", id, MaxHeaderBytes)
		case errors.Is(err, io.EOF):
			return "", fmt.Errorf("pack %s header: %w", id, io.ErrUnexpectedEOF)
		case err != nil:
			return "", err
		}
		return string(s), nil
	}
	first, err := line()
	if err != nil {
		return nil, err
	}
	if first != magic {
		return nil, fmt.Errorf("pack %s is not a dagger pack", id)
	}
	count, err := line()
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(strings.TrimSuffix(count, "\n"))
	if err != nil || n < 1 || n > MaxMembers {
		return nil, fmt.Errorf("pack %s has an invalid member count", id)
	}
	members := make([]Member, 0, n)
	for range n {
		s, err := line()
		if err != nil {
			return nil, err
		}
		dgst, size, ok := strings.Cut(strings.TrimSuffix(s, "\n"), " ")
		if !ok {
			return nil, fmt.Errorf("pack %s has an invalid member line", id)
		}
		m := Member{Digest: digest.Digest(dgst)}
		if m.Size, err = strconv.ParseInt(size, 10, 64); err != nil {
			return nil, fmt.Errorf("pack %s has an invalid member size", id)
		}
		members = append(members, m)
	}
	header, err := Header(members)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", id, err)
	}
	if !bytes.Equal(raw.Bytes(), header) || digest.SHA256.FromBytes(header) != id {
		return nil, fmt.Errorf("pack header does not match its ID %s", id)
	}
	return members, nil
}
