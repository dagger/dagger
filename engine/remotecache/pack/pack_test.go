package pack

import (
	"bufio"
	"bytes"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func testMembers(blobs ...string) []Member {
	members := make([]Member, len(blobs))
	for i, b := range blobs {
		members[i] = Member{Digest: digest.FromString(b), Size: int64(len(b))}
	}
	return members
}

// A pack is its header then its members; reading it back gives the members
// and leaves the reader at the first member's bytes.
func TestPackRoundTrip(t *testing.T) {
	members := testMembers("one", "two two", "")
	header, err := Header(members)
	require.NoError(t, err)
	require.Equal(t, "dagger-pack v1\n3\n"+
		members[0].Digest.String()+" 3\n"+
		members[1].Digest.String()+" 7\n"+
		members[2].Digest.String()+" 0\n", string(header))
	id, err := ID(members)
	require.NoError(t, err)
	require.Equal(t, digest.FromBytes(header), id)
	size, err := Size(members)
	require.NoError(t, err)
	require.Equal(t, int64(len(header)+10), size)

	r := bufio.NewReader(io.MultiReader(bytes.NewReader(header), strings.NewReader("onetwo two")))
	got, err := ReadHeader(r, id)
	require.NoError(t, err)
	require.Equal(t, members, got)
	rest, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, "onetwo two", string(rest))
}

func TestPackHeaderRefuses(t *testing.T) {
	ok := testMembers("a")[0]
	for name, members := range map[string][]Member{
		"no members": nil,
		"twice":      {ok, ok},
		"negative":   {{Digest: ok.Digest, Size: -1}},
		"not sha256": {{Digest: digest.SHA512.FromString("a"), Size: 1}},
		"invalid":    {{Digest: "sha256:zz", Size: 1}},
		"too large":  {{Digest: ok.Digest, Size: MaxBytes}},
		// Sizes that would wrap a sum below the limit.
		"overflow": {{Digest: ok.Digest, Size: math.MaxInt64}, {Digest: digest.FromString("b"), Size: math.MaxInt64}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Header(members)
			require.Error(t, err)
		})
	}
}

// A header that is cut short, malformed, or not the one the ID names is
// refused.
func TestPackReadHeaderRefuses(t *testing.T) {
	members := testMembers("one", "two")
	header, err := Header(members)
	require.NoError(t, err)
	id, err := ID(members)
	require.NoError(t, err)
	other, err := ID(testMembers("one"))
	require.NoError(t, err)
	for name, tc := range map[string]struct {
		data string
		id   digest.Digest
	}{
		"truncated":   {data: string(header[:len(header)-5]), id: id},
		"wrong ID":    {data: string(header), id: other},
		"not a pack":  {data: "something else\n", id: id},
		"bad count":   {data: "dagger-pack v1\nx\n", id: id},
		"zero count":  {data: "dagger-pack v1\n0\n", id: id},
		"padded":      {data: strings.Replace(string(header), "\n2\n", "\n02\n", 1), id: id},
		"bad line":    {data: "dagger-pack v1\n1\nnospace\n", id: id},
		"invalid ID":  {data: string(header), id: "sha256:nope"},
		"long line":   {data: "dagger-pack v1\n1\n" + strings.Repeat("x", 8192) + "\n", id: id},
		"empty input": {data: "", id: id},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadHeader(bufio.NewReader(strings.NewReader(tc.data)), tc.id)
			require.Error(t, err)
		})
	}
}
