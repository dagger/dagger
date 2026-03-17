package dagql

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResultCallDigestErrorsDoNotPanic(t *testing.T) {
	t.Parallel()

	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "broken",
		Args: []*ResultCallArg{{
			Name: "bad",
			Value: &ResultCallLiteral{
				Kind: ResultCallLiteralKind("bogus"),
			},
		}},
	}

	_, err := frame.RecipeDigest()
	require.ErrorContains(t, err, `args: failed to write argument "bad" to hash`)

	_, err = frame.ContentPreferredDigest()
	require.ErrorContains(t, err, `args: failed to write argument "bad" to hash`)

	_, _, err = frame.SelfDigestAndInputRefs()
	require.ErrorContains(t, err, `result call frame "broken" args: failed to write argument "bad" to hash`)
}
