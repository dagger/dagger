package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/dotenv"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type EnvFileSuite struct{}

func TestEnvFile(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(EnvFileSuite{})
}

func (EnvFileSuite) TestNamespace(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name      string
		input     map[string]string
		namespace string
		output    map[string]string
	}{
		{
			"simple",
			map[string]string{
				"animal_name":    "daisy",
				"animal_species": "dog",
				"foo":            "bar",
			},
			"animal",
			map[string]string{
				"name":    "daisy",
				"species": "dog",
			},
		},
		{
			"simple with underscore suffix",
			map[string]string{
				"animal_name":    "daisy",
				"animal_species": "dog",
				"foo":            "bar",
			},
			"animal_",
			map[string]string{
				"name":    "daisy",
				"species": "dog",
			},
		},
		{
			"case insensitive",
			map[string]string{
				"ANIMAL_name":    "daisy",
				"Animal_species": "dog",
				"foo":            "bar",
			},
			"animal",
			map[string]string{
				"name":    "daisy",
				"species": "dog",
			},
		},
		{
			"underscores",
			map[string]string{
				"my_app_token": "topsecret",
				"my_app_name":  "petstore",
				"foo":          "bar",
			},
			"my_app",
			map[string]string{
				"token": "topsecret",
				"name":  "petstore",
			},
		},
		{
			"dashes + underscores",
			map[string]string{
				"my_app_token": "topsecret",
				"myApp_name":   "petstore",
				"foo":          "bar",
				"MY_aPp_URL":   "http://localhost",
			},
			"my-app",
			map[string]string{
				"token": "topsecret",
				"name":  "petstore",
				"URL":   "http://localhost",
			},
		},
		{
			"underscore in prefix + at the end of prefix",
			map[string]string{
				"my_app_token": "topsecret",
				"my_app_name":  "petstore",
				"foo":          "bar",
			},
			"my_app_",
			map[string]string{
				"token": "topsecret",
				"name":  "petstore",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			input := c.EnvFile()
			for k, v := range tc.input {
				input = input.WithVariable(k, v)
			}
			output := input.Namespace(tc.namespace)
			outputMap := envFileMap(t, output)
			require.Equal(t, tc.output, outputMap)
		})
	}
}

func envFileMap(t *testctx.T, env *dagger.EnvFile) map[string]string {
	ctx := t.Context()
	result := map[string]string{}
	vars, err := env.Variables(ctx)
	require.NoError(t, err)
	for _, v := range vars {
		name, err := v.Name(ctx)
		require.NoError(t, err)
		value, err := v.Value(ctx)
		require.NoError(t, err)
		result[name] = value
	}
	return result
}

// Test that the Dagger type matches the behavior of the underlying dotenv library,
// when evaluating values.
// This covers variable expansion, quotes and escape handling, etc.
//
// Since the underlying library is well-tested, this avoids duplicating those tests here.
// Instead we can focus on tests specific to this layer.
func (EnvFileSuite) TestEvalMatch(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name string
		vars map[string]string
	}{
		{
			name: "simple",
			vars: map[string]string{
				"FOO":     "bar",
				"hello":   "world",
				"StoRy":   "once upon a time...",
				"various": "lots of (weird) characters &^",
			},
		},
		{
			name: "expansion",
			vars: map[string]string{
				"animal":    "dog",
				"superhero": "${animal}man",
				"message":   "hello, nice $animal !",
				"message2":  "hello, nice ${animal}",
				"message3":  "hello, nice '${animal}'",
			},
		},
		{
			name: "quoted_values",
			vars: map[string]string{
				"animal":            `"dog"`,
				"message":           `"hello, nice ${animal}"`,
				"message2":          `"hello, nice $animal"`,
				"story":             `"once upon a time, there was a man who said $message"`,
				"QUOTES":            `"this sentence is double-quoted (with \"), 'and this is single-quoted'"`,
				"single_quoted_var": `"hello, nice '$animal'"`,
			},
		},
		{
			name: "raw",
			vars: map[string]string{
				"simple":        "hello world",
				"quotes":        `"hello world"`,
				"single_quotes": `'hello world'`,
				"dollar_sign":   "$FOO",
				"expansion":     "${FOO}",
				"command_sub":   "$(echo hello)",
				"backticks":     "`echo hello`",
				"backslash":     `hello\nworld`,
				"mixed":         `"$FOO ${BAR} $(cmd)" and 'more'`,
				"special_chars": "()&^%$#@!",
			},
		},
		{
			name: "remove_referenced_variable",
			vars: map[string]string{
				"GREETING": "bonjour",
				"NAME":     "monde",
				"message":  "$GREETING, $NAME!",
			},
		},
		{
			name: "get_unset",
			vars: map[string]string{
				"FOO":   "bar",
				"HELLO": "world",
				"DOG":   "${FOO}-${HELLO}",
			},
		},
		{
			name: "override",
			vars: map[string]string{
				"FOO": "newbar",
			},
		},
		{
			name: "file",
			vars: map[string]string{
				"animal":            "dog",
				"message":           "hello, nice ${animal}",
				"message2":          `"hello, nice $animal"`,
				"story":             "once upon a time, there was a man who said $message",
				"QUOTED":            `"this sentence is double-quoted (with \"), 'and this is single-quoted'"`,
				"single_quoted_var": `"hello, nice '$animal'"`,
			},
		},

		{
			name: "JSON array must be protected with quotes",
			vars: map[string]string{
				`no_quotes`:     `["ga", "bu", "zo", "meu", 42]`,
				`single_quotes`: `'["ga", "bu", "zo", "meu", 42]'`,
				`double_quotes`: `"[\"ga\", \"bu\", \"zo\", \"meu\", 42]"`,
			},
		},
		{
			name: "JSON object must be protected with quotes",
			vars: map[string]string{
				`no_quotes`:     `{"name": "John Wick", "age": 58, "occupation": "assassin"}`,
				`single_quotes`: `'{"name": "John Wick", "age": 58, "occupation": "assassin"}'`,
				`double_quotes`: `"{\"name\": \"John Wick\", \"age\": 58, \"occupation\": \"assassin\"}"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			env := c.EnvFile()
			var environ []string
			for name, inputValue := range tt.vars {
				environ = append(environ, name+"="+inputValue)
				env = env.WithVariable(name, inputValue)
			}

			for name := range tt.vars {
				expectedValue, expectedFound, expectedErr := dotenv.Lookup(environ, name, nil)
				if !expectedFound {
					expectedValue = ""
				}
				actualValue, actualErr := env.Get(ctx, name)
				if expectedErr != nil {
					require.Error(t, actualErr, tt.name)
				} else {
					require.NoError(t, actualErr, tt.name)
				}
				if expectedErr == nil {
					require.Equal(t, expectedValue, actualValue, tt.name)
				}
			}
		})
	}
}

func (EnvFileSuite) TestFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	inputFile := c.File(".env", `animal=dog
message=hello, nice ${animal}
message2="hello, nice $animal"
story=once upon a time, there was a man who said $message
QUOTED="this sentence is double-quoted (with \"), 'and this is single-quoted'"
single_quoted_var="hello, nice '$animal'"
`)
	env := inputFile.AsEnvFile()
	outputFile := env.AsFile()
	inputContents, err := inputFile.Contents(ctx)
	require.NoError(t, err)
	outputContents, err := outputFile.Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, inputContents, outputContents)
}

func (EnvFileSuite) TestRemoveReferencedVariable(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	env := c.EnvFile().
		WithVariable("GREETING", "bonjour").
		WithVariable("NAME", "monde").
		WithVariable("message", `$GREETING, $NAME!`)

	before, err := env.Get(ctx, "message")
	require.NoError(t, err)
	require.Equal(t, `bonjour, monde!`, before)

	env = env.WithoutVariable("NAME")

	_, err = env.Get(ctx, "message")
	require.Error(t, err)

	afterRaw, err := env.Get(ctx, "message", dagger.EnvFileGetOpts{Raw: true})
	require.NoError(t, err)
	require.Equal(t, `$GREETING, $NAME!`, afterRaw)
}

// Test overriding an existing variable with a new value
func (EnvFileSuite) TestOverride(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	envFile := c.EnvFile().
		WithVariable("FOO", "bar").
		WithVariable("FOO", "newbar")

	variable, err := envFile.Get(ctx, "FOO")
	require.NoError(t, err)
	require.Equal(t, "newbar", variable)
}

func (EnvFileSuite) TestSystemVariables(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := nestedDaggerContainer(t, c, "", "").
		WithEnvVariable("SYSTEM_GREETING", "live long and prosper").
		WithNewFile(".env", `GREETING="${SYSTEM_GREETING}"`)
	output1, err := ctr.
		WithExec([]string{
			"dagger", "core", "host", "file", "--path=.env", "as-env-file", "get", "GREETING"},
			nestedExec,
		).
		Stdout(ctx)
	// FIXME System env variable lookup is temporarily disabled
	// see https://github.com/dagger/dagger/pull/11034#discussion_r2401382370
	require.Error(t, err)
	require.NotEqual(t, "live long and prosper", output1, "output should NOT include the system env variable (feature temporarily disabled)")
	output2, err := ctr.
		WithExec([]string{
			"dagger", "core", "host", "file", "--path=.env", "as-env-file", "variables", "value"},
			nestedExec,
		).
		Stdout(ctx)
	// FIXME System env variable lookup is temporarily disabled
	// see https://github.com/dagger/dagger/pull/11034#discussion_r2401382370
	require.Error(t, err)
	require.NotEqual(t, "live long and prosper", output2, "output should NOT include the system env variable (feature temporarily disabled)")
}
