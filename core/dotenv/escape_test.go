package dotenv_test

import (
	"fmt"
	"testing"

	"github.com/dagger/dagger/core/dotenv"
	"github.com/stretchr/testify/require"
)

func TestEscape(t *testing.T) {
	for i, tt := range []struct {
		input  string
		output string
	}{
		{
			input:  `{"foo": "bar"}`,
			output: `{\"foo\": \"bar\"}`,
		},
		{
			input:  `"it's ok"`,
			output: `\"it\'s ok\"`,
		},
		{
			input:  `"hello $(echo "world")"`,
			output: `\"hello \$\(echo \"world\"\)\"`,
		},
		{
			input:  `"hello $foo"`,
			output: `\"hello \$foo\"`,
		},
		{
			input:  `"it's ok"`,
			output: `\"it\'s ok\"`,
		},
		{
			input:  `hello '"world"'`,
			output: `hello \'\"world\"\'`,
		},
		{
			input:  `'"\`,
			output: `\'\"\\`,
		},
		{
			input:  `:')`,
			output: `:\'\)`,
		},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			require.Equal(t, tt.output, dotenv.Escape(tt.input))
		})
	}
}
