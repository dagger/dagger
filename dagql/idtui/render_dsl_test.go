package idtui

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"

	callpbv1 "github.com/dagger/dagger/dagql/call/callpbv1"
)

func TestRenderDSLSource(t *testing.T) {
	for _, field := range []string{"withDirectory", "withFile", "withMountedDirectory", "withMountedFile"} {
		t.Run(field, func(t *testing.T) {
			for _, callSource := range []bool{false, true} {
				name := "scalar"
				source := &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "source"}}
				if callSource {
					name = "call"
					source = &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: "xxh3:source"}}
				}
				t.Run(name, func(t *testing.T) {
					call := &callpbv1.Call{
						Field: field,
						Args: []*callpbv1.Argument{
							{Name: "path", Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "/dest"}}},
							{Name: "source", Value: source},
						},
					}
					// A nil DB makes any attempt to expand source fail immediately.
					r := &renderer{}
					out := termenv.NewOutput(io.Discard, termenv.WithProfile(termenv.Ascii))
					title, elided, special := r.renderFieldCall(call, out, "", 0)
					if callSource {
						require.False(t, special, "call-valued source must use generic rendering")
						require.Empty(t, title)
						require.Nil(t, elided)
					} else {
						require.True(t, special)
						require.Equal(t, field+" /dest <- source", title)
						require.Equal(t, map[string]struct{}{"path": {}, "source": {}}, elided)
					}
				})
			}
		})
	}
}

func TestRenderDSLUnknownCallArgument(t *testing.T) {
	call := &callpbv1.Call{
		Field: "withExec",
		Args: []*callpbv1.Argument{
			{Name: "args", Value: &callpbv1.Literal{Value: &callpbv1.Literal_List{List: &callpbv1.List{
				Values: []*callpbv1.Literal{
					{Value: &callpbv1.Literal_String_{String_: "echo"}},
					{Value: &callpbv1.Literal_String_{String_: "hello"}},
				},
			}}}},
			{Name: "unknown", Value: &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: "xxh3:unavailable"}}},
		},
	}
	// Unknown arguments are ignored without consulting the (nil) DB.
	r := &renderer{}
	out := termenv.NewOutput(io.Discard, termenv.WithProfile(termenv.Ascii))
	title, elided, special := r.renderFieldCall(call, out, "", 0)
	require.True(t, special)
	require.Equal(t, "withExec echo hello", title)
	require.Equal(t, map[string]struct{}{"args": {}}, elided)
}

func TestRenderDSLCallReferenceJSON(t *testing.T) {
	data, err := callArgsToJSON(&callpbv1.Call{
		Args: []*callpbv1.Argument{
			{Name: "source", Value: &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: "xxh3:source"}}},
		},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"source":{"callDigest":"xxh3:source"}}`, string(data))
	var args WithDirectoryArgs
	var typeErr *json.UnmarshalTypeError
	require.ErrorAs(t, json.Unmarshal(data, &args), &typeErr)
	require.Equal(t, "source", typeErr.Field)
}

func TestRenderDSLSourceStyleIsNotText(t *testing.T) {
	// The old conversion rendered into a buffer, but marshaled out.String(),
	// which is a termenv.Style rather than the buffer's text. Preserve the
	// resulting string-field rejection, not a potentially huge inline title.
	var buf strings.Builder
	out := termenv.NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	_, err := io.WriteString(out, "directory.withFile(...)")
	require.NoError(t, err)
	require.NotEmpty(t, buf.String())
	data, err := json.Marshal(map[string]any{"source": out.String()})
	require.NoError(t, err)
	var args WithDirectoryArgs
	var typeErr *json.UnmarshalTypeError
	require.ErrorAs(t, json.Unmarshal(data, &args), &typeErr)
	require.Equal(t, "source", typeErr.Field)
}
