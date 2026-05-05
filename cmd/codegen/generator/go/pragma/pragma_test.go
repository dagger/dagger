package pragma_test

import (
	"reflect"
	"testing"

	"github.com/dagger/dagger/cmd/codegen/generator/go/pragma"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name     string
		comment  string
		expected map[string]any
		rest     string
	}{
		{
			name:     "empty",
			comment:  "",
			expected: map[string]any{},
			rest:     "",
		},
		{
			name:     "no pragmas",
			comment:  "hello world\n",
			expected: map[string]any{},
			rest:     "hello world\n",
		},
		{
			name:    "single bare flag",
			comment: "+optional\n",
			expected: map[string]any{
				"optional": nil,
			},
			rest: "",
		},
		{
			name:    "string default",
			comment: "+default=\"hi\"\n",
			expected: map[string]any{
				"default": "hi",
			},
			rest: "",
		},
		{
			name:    "int default",
			comment: "+default=42\n",
			expected: map[string]any{
				"default": float64(42),
			},
			rest: "",
		},
		{
			name:    "optional and default combined",
			comment: "+optional\n+default=\"Hello\"\n",
			expected: map[string]any{
				"optional": nil,
				"default":  "Hello",
			},
			rest: "",
		},
		{
			name:    "leading description plus pragmas",
			comment: "the message to echo\n+optional\n+default=\"en\"\n",
			expected: map[string]any{
				"optional": nil,
				"default":  "en",
			},
			rest: "the message to echo\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, rest := pragma.Parse(tc.comment)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("data: got %#v, want %#v", got, tc.expected)
			}
			if rest != tc.rest {
				t.Errorf("rest: got %q, want %q", rest, tc.rest)
			}
		})
	}
}
