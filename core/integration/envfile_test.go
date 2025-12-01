package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/dotenv"
	"github.com/dagger/dagger/internal/testutil"
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
			name: "raw_simple",
			vars: map[string]string{
				"simple": "hello world",
			},
		},
		{
			name: "raw_quotes",
			vars: map[string]string{
				"quotes": `"hello world"`,
			},
		},
		{
			name: "raw_single_quotes",
			vars: map[string]string{
				"single_quotes": `'hello world'`,
			},
		},
		{
			name: "raw_dollar_sign",
			vars: map[string]string{
				"dollar_sign": "$FOO",
			},
		},
		{
			name: "raw_expansion",
			vars: map[string]string{
				"expansion": "${FOO}",
			},
		},
		{
			name: "raw_command_sub",
			vars: map[string]string{
				"command_sub": "$(echo hello)",
			},
		},
		{
			name: "raw_backticks",
			vars: map[string]string{
				"backticks": "`echo hello`",
			},
		},
		{
			name: "raw_backslash",
			vars: map[string]string{
				"backslash": `hello\nworld`,
			},
		},
		{
			name: "raw_mixed",
			vars: map[string]string{
				"mixed": `"$FOO ${BAR} $(cmd)" and 'more'`,
			},
		},
		{
			name: "raw_special_chars",
			vars: map[string]string{
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
			"dagger", "core", "host", "file", "--path=.env", "as-env-file", "get", "--name", "GREETING"},
			nestedExec,
		).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "live long and prosper", output1)
	output2, err := ctr.
		WithExec([]string{
			"dagger", "core", "host", "file", "--path=.env", "as-env-file", "variables", "value"},
			nestedExec,
		).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "live long and prosper\n", output2)
}

func (EnvFileSuite) TestSystemVariableCachePolicy(ctx context.Context, t *testctx.T) {
	tmp := tempDirWithEnvFile(t,
		`NAME=${MYNAME}`,
	)
	for _, userName := range []string{"user1", "user2"} {
		c := connect(ctx, t, dagger.WithWorkdir(tmp), dagger.WithEnvironmentVariable("MYNAME", userName))
		s, err := c.Host().File(".env").AsEnvFile().Get(ctx, "NAME")
		require.NoError(t, err)
		require.Equal(t, userName, s)
	}
}

func (EnvFileSuite) TestCaching(ctx context.Context, t *testctx.T) {
	tmp := tempDirWithEnvFile(t,
		`NAME=${MYNAME}`,
	)
	seenData := map[string]string{}
	for i := 0; i < 2; i++ {
		for _, userName := range []string{"user1", "user2"} {
			c := connect(ctx, t, dagger.WithWorkdir(tmp), dagger.WithEnvironmentVariable("MYNAME", userName))
			ef := c.Host().File(".env").AsEnvFile()
			s, err := c.Container().From(alpineImage).WithEnvFileVariables(ef).
				WithExec([]string{"sh", "-c", "echo -n \"Hello $NAME here is some random data: \" && cat /dev/urandom | head -c 15 | base64 -w0"}).Stdout(ctx)
			require.NoError(t, err)
			expectedPrefix := fmt.Sprintf("Hello %s here is some random data: ", userName)
			testutil.HasPrefix(t, expectedPrefix, s)
			data := strings.TrimPrefix(s, expectedPrefix)
			require.Len(t, data, 15*4/3) // base64 encoding bloats the data by 4/3
			if i == 0 {
				seenData[userName] = data
			} else {
				d, found := seenData[userName]
				require.Equal(t, true, found)
				require.Equal(t, d, data, "expected cached execution; however, different random data indicates it was re-executed")
			}
		}
	}
}

func (EnvFileSuite) TestCachingWithIndirectVar(ctx context.Context, t *testctx.T) {
	tmp := tempDirWithEnvFile(t,
		`MYVAR=NOT_USED`,
	)
	seenData := map[string]string{}
	for i := 0; i < 2; i++ {
		for _, userName := range []string{"user1", "user2"} {
			c := connect(ctx, t, dagger.WithWorkdir(tmp), dagger.WithEnvironmentVariable("MYNAME", userName))
			ef := c.Host().File(".env").AsEnvFile().WithVariable("NAME", "$MYNAME")
			s, err := c.Container().From(alpineImage).WithEnvFileVariables(ef).
				WithExec([]string{"sh", "-c", "echo -n \"Hello $NAME here is some random data: \" && cat /dev/urandom | head -c 15 | base64 -w0"}).Stdout(ctx)
			require.NoError(t, err)
			expectedPrefix := fmt.Sprintf("Hello %s here is some random data: ", userName)
			testutil.HasPrefix(t, expectedPrefix, s)
			data := strings.TrimPrefix(s, expectedPrefix)
			require.Len(t, data, 15*4/3) // base64 encoding bloats the data by 4/3
			if i == 0 {
				seenData[userName] = data
			} else {
				d, found := seenData[userName]
				require.Equal(t, true, found)
				require.Equal(t, d, data, "expected cached execution; however, different random data indicates it was re-executed")
			}
		}
	}
}

func (EnvFileSuite) TestSecretFile(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()

	initCmd := hostDaggerCommand(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
	initOutput, err := initCmd.CombinedOutput()
	require.NoError(t, err, string(initOutput))

	err = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main
import (
	"context"
)

type Test struct {}

func (m *Test) Foo(ctx context.Context) (string, error) {
	return dag.Dep().Bar(ctx)
}
`), 0644)
	require.NoError(t, err)

	depDir := filepath.Join(modDir, "dep")
	require.NoError(t, os.Mkdir(depDir, 0755))

	initDepCmd := hostDaggerCommand(ctx, t, depDir, "init", "--source=.", "--name=dep", "--sdk=go")
	initDepOutput, err := initDepCmd.CombinedOutput()
	require.NoError(t, err, string(initDepOutput))

	err = os.WriteFile(filepath.Join(depDir, "main.go"), []byte(`package main
import (
	"context"
	"encoding/base64"

	"dagger/dep/internal/dagger"
)

type Dep struct {}

func (m *Dep) Bar(
	ctx context.Context, 
	// +optional
	s *dagger.Secret,
) (string, error) {
	pt, err := s.Plaintext(ctx)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(pt)), nil
}
`), 0644)
	require.NoError(t, err)

	installCmd := hostDaggerCommand(ctx, t, modDir, "install", depDir)
	installOutput, err := installCmd.CombinedOutput()
	require.NoError(t, err, string(installOutput))

	secretFile := filepath.Join(depDir, "topsecret.txt")
	err = os.WriteFile(secretFile, []byte(`doodoo`), 0644)
	require.NoError(t, err)

	envFile := filepath.Join(depDir, ".env")
	err = os.WriteFile(envFile, []byte(`bar_s=file://`+secretFile), 0644)
	require.NoError(t, err)

	callCmd := hostDaggerCommand(ctx, t, modDir, "call", "-s", "foo")
	callOutput, err := callCmd.CombinedOutput()
	require.NoError(t, err, string(callOutput))
	// the CLI spams "user default: ..." messages despite -s, get the last line only
	lastLine := callOutput
	if idx := strings.LastIndex(string(callOutput), "\n"); idx != -1 {
		lastLine = callOutput[idx+1:]
	}
	decodeOutput, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(lastLine)))
	require.NoError(t, err, string(callOutput))
	require.Equal(t, "doodoo", string(decodeOutput))
}
