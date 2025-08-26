package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type EnvFileSuite struct{}

func TestEnvFile(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(EnvFileSuite{})
}

// Test converting a file to an environment file and reading its variables
func (EnvFileSuite) TestAsEnvFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	requireEnvFileEqual(ctx, t,
		c.File(".env",
			`FOO=bar
HELLO=world
DOG=${FOO}-${HELLO}
`).AsEnvFile(),
		map[string]string{
			"FOO":   "bar",
			"HELLO": "world",
			"DOG":   "${FOO}-${HELLO}", // expansion disabled
		})
}

// Test environment variable expansion when enabled from a file
func (EnvFileSuite) TestFileExpand(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	env := c.File(".env", `toosoon=hello $animal
animal=dog
message=hello, nice ${animal}
story=once upon a time, there was a man who said $message
`).AsEnvFile(dagger.FileAsEnvFileOpts{
		Expand: true,
	})
	expected := map[string]string{
		"toosoon": "hello ",
		"animal":  "dog",
		"message": "hello, nice dog",
		"story":   "once upon a time, there was a man who said hello, nice dog",
	}
	requireEnvFileEqual(ctx, t, env, expected)
}

func (EnvFileSuite) TestQuotes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	expected := map[string]string{
		"partial":  `The hero said: "beware of dragons!"`,
		"complete": `"This sentence is entirely quotes"`,
	}
	t.Run("created inline, expanded", func(ctx context.Context, t *testctx.T) {
		env := c.
			EnvFile(dagger.EnvFileOpts{Expand: true}).
			WithVariable("partial", `The hero said: "beware of dragons!"`).
			WithVariable("complete", `"This sentence is entirely quotes"`)
		requireEnvFileEqual(ctx, t, env, expected)
	})
	t.Run("created inline, not expanded", func(ctx context.Context, t *testctx.T) {
		env := c.
			EnvFile(dagger.EnvFileOpts{Expand: false}).
			WithVariable("partial", `The hero said: "beware of dragons!"`).
			WithVariable("complete", `"This sentence is entirely quotes"`)
		requireEnvFileEqual(ctx, t, env, expected)
	})
	t.Run("created from file, expanded", func(ctx context.Context, t *testctx.T) {
		env := c.
			File(
				".env",
				`partial=The hero said: "beware of dragons!"
complete="This sentence is entirely quotes"`).
			AsEnvFile(dagger.FileAsEnvFileOpts{Expand: true})
		requireEnvFileEqual(ctx, t, env, expected)
	})
	t.Run("created from file, not expanded", func(ctx context.Context, t *testctx.T) {
		env := c.
			File(
				".env",
				`partial=The hero said: "beware of dragons!"
complete="This sentence is entirely quotes"`).
			AsEnvFile(dagger.FileAsEnvFileOpts{Expand: false})
		requireEnvFileEqual(ctx, t, env, expected)
	})
}

// Test environment variable expansion when enabled
func (EnvFileSuite) TestExpand(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	env := c.
		EnvFile(dagger.EnvFileOpts{
			Expand: true,
		}).
		WithVariable("toosoon", "hello $animal").
		WithVariable("animal", "dog").
		WithVariable("message", "hello, nice ${animal}").
		WithVariable("story", "once upon a time, there was a man who said $message")
	expected := map[string]string{
		"toosoon": "hello ",
		"animal":  "dog",
		"message": "hello, nice dog",
		"story":   "once upon a time, there was a man who said hello, nice dog",
	}
	requireEnvFileEqual(ctx, t, env, expected)
}

// Test environment variable expansion when disabled
func (EnvFileSuite) TestNoExpand(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	env := c.
		EnvFile(dagger.EnvFileOpts{
			Expand: false,
		}).
		WithVariable("toosoon", "hello $animal").
		WithVariable("animal", "dog").
		WithVariable("message", "hello, nice ${animal}").
		WithVariable("story", `"once upon a time, there was a man who said $message"`).
		WithVariable("meta", `'this file sets $toosoon, ${animal}, $message and $story'`)
	expected := map[string]string{
		"toosoon": "hello $animal",
		"animal":  "dog",
		"message": "hello, nice ${animal}",
		"story":   `"once upon a time, there was a man who said $message"`,
		"meta":    `'this file sets $toosoon, ${animal}, $message and $story'`,
	}
	requireEnvFileEqual(ctx, t, env, expected)
}

// Test adding variables to an environment file
func (EnvFileSuite) TestWithVariable(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	env := c.EnvFile().
		WithVariable("FOO", "bar").
		WithVariable("HELLO", "world").
		WithVariable("DOG", "${FOO}-${HELLO}")
	expected := map[string]string{
		"FOO":   "bar",
		"HELLO": "world",
		"DOG":   "${FOO}-${HELLO}", // expand is disabled
	}
	requireEnvFileEqual(ctx, t, env, expected)
}

// Test removing variables from an environment file
func (EnvFileSuite) TestWithoutVariable(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	env := c.EnvFile().
		WithVariable("FOO", "bar").
		WithVariable("HELLO", "world").
		WithoutVariable("FOO")
	expected := map[string]string{
		"HELLO": "world",
	}
	requireEnvFileEqual(ctx, t, env, expected)
}

func (EnvFileSuite) TestUnknown(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	env := c.EnvFile().
		WithVariable("FOO", "bar").
		WithVariable("HELLO", "world").
		WithVariable("DOG", "${FOO}-${HELLO}")
	variable, err := env.Get(ctx, "UNKNOWN")
	require.NoError(t, err)
	require.Empty(t, variable)
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

// Test converting an environment file back to a regular file
func (EnvFileSuite) TestEnvToFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	envFile := c.EnvFile().
		WithVariable("FOO", "bar").
		WithVariable("HELLO", "world")

	contents, err := envFile.AsFile().Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "FOO=bar\nHELLO=world\n", contents)
}

func requireEnvFileEqual(ctx context.Context, t *testctx.T, ef *dagger.EnvFile, expectedVars map[string]string) {
	var expectedNames []string
	for name, expectedValue := range expectedVars {
		expectedNames = append(expectedNames, name)
		requireEnvFileVariableEqual(ctx, t, ef, name, expectedValue)
	}
	requireEnvFileKeysEqual(ctx, t, ef, expectedNames...)
	vars, err := ef.Variables(ctx)
	require.NoError(t, err)
	for _, envVar := range vars {
		name, err := envVar.Name(ctx)
		require.NoError(t, err)
		value, err := envVar.Value(ctx)
		require.NoError(t, err)
		_, expected := expectedVars[name]
		require.True(t, expected)
		require.Equal(t, expectedVars[name], value)
	}
}

func requireEnvFileKeysEqual(ctx context.Context, t *testctx.T, ef *dagger.EnvFile, expectedKeys ...string) {
	keys := map[string]bool{}
	for _, key := range expectedKeys {
		keys[key] = false
	}
	vars, err := ef.Variables(ctx)
	require.NoError(t, err)
	for _, v := range vars {
		name, err := v.Name(ctx)
		require.NoError(t, err)
		_, expected := keys[name]
		require.True(t, expected, "envfile contains unexpected key: %q", name)
		keys[name] = true
	}
	for key, found := range keys {
		require.True(t, found, "envfile is missing expected key: %q", key)
	}
}

func requireEnvFileVariableEqual(ctx context.Context, t *testctx.T, ef *dagger.EnvFile, name, expectedValue string) {
	value, err := ef.Get(ctx, name)
	require.NoError(t, err)
	require.Equal(t, expectedValue, value)
}
