package callpbv1

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// Encode is the base64 of the call's deterministic protobuf, whatever it
// encoded before.
func TestEncodeIsBase64OfDeterministicProtobuf(t *testing.T) {
	call := func(field, value string) *Call {
		return &Call{
			Field: field,
			Type:  &Type{NamedType: "Thing"},
			Args: []*Argument{{
				Name:  "value",
				Value: &Literal{Value: &Literal_String_{String_: value}},
			}},
			Digest: "xxh3:" + field,
		}
	}
	for _, c := range []*Call{
		call("large", strings.Repeat("x", 4096)),
		call("small", "y"),
		call("empty", ""),
	} {
		pb, err := proto.MarshalOptions{Deterministic: true}.Marshal(c)
		require.NoError(t, err)
		got, err := c.Encode()
		require.NoError(t, err)
		require.Equal(t, base64.StdEncoding.EncodeToString(pb), got)

		var decoded Call
		require.NoError(t, decoded.Decode(got))
		require.True(t, proto.Equal(c, &decoded))
	}
}
