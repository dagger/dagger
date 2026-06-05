package core

// These tests cover TOML values, mirroring the JSON value tests. They verify
// scalar, table and array values, plus format-preserving edits of TOML
// documents.

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type TOMLSuite struct{}

func TestTOML(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(TOMLSuite{})
}

func (TOMLSuite) TestInteger(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	tomlInt := c.TOML().NewInteger(42)
	value, err := tomlInt.AsInteger(ctx)
	require.NoError(t, err)
	require.Equal(t, 42, value)

	tomlNegInt := c.TOML().NewInteger(-123)
	negValue, err := tomlNegInt.AsInteger(ctx)
	require.NoError(t, err)
	require.Equal(t, -123, negValue)

	tomlZero := c.TOML().NewInteger(0)
	zeroValue, err := tomlZero.AsInteger(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, zeroValue)
}

func (TOMLSuite) TestString(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	tomlStr := c.TOML().NewString("hello world")
	value, err := tomlStr.AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello world", value)

	tomlEmpty := c.TOML().NewString("")
	emptyValue, err := tomlEmpty.AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "", emptyValue)

	tomlSpecial := c.TOML().NewString("hello\nworld\t\"quotes\"")
	specialValue, err := tomlSpecial.AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello\nworld\t\"quotes\"", specialValue)
}

func (TOMLSuite) TestBoolean(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	tomlTrue := c.TOML().NewBoolean(true)
	trueValue, err := tomlTrue.AsBoolean(ctx)
	require.NoError(t, err)
	require.Equal(t, true, trueValue)

	tomlFalse := c.TOML().NewBoolean(false)
	falseValue, err := tomlFalse.AsBoolean(ctx)
	require.NoError(t, err)
	require.Equal(t, false, falseValue)
}

func (TOMLSuite) TestArray(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// TOML arrays live inside a table; extract the array via a field.
	tomlArray := c.TOML().
		WithContents(dagger.TOML(`values = [1, "hello", true]`)).
		Field([]string{"values"})

	arrayValues, err := tomlArray.AsArray(ctx)
	require.NoError(t, err)
	require.Len(t, arrayValues, 3)

	firstValue, err := arrayValues[0].AsInteger(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, firstValue)

	secondValue, err := arrayValues[1].AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", secondValue)

	thirdValue, err := arrayValues[2].AsBoolean(ctx)
	require.NoError(t, err)
	require.Equal(t, true, thirdValue)
}

func (TOMLSuite) TestNestedPaths(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	nestedTOML := `
[user]
name = "John"
age = 30

[user.profile]
email = "john@example.com"
active = true
`
	tomlObj := c.TOML().WithContents(dagger.TOML(nestedTOML))

	nameValue, err := tomlObj.Field([]string{"user", "name"}).AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "John", nameValue)

	ageValue, err := tomlObj.Field([]string{"user", "age"}).AsInteger(ctx)
	require.NoError(t, err)
	require.Equal(t, 30, ageValue)

	emailValue, err := tomlObj.Field([]string{"user", "profile", "email"}).AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "john@example.com", emailValue)

	activeValue, err := tomlObj.Field([]string{"user", "profile", "active"}).AsBoolean(ctx)
	require.NoError(t, err)
	require.Equal(t, true, activeValue)
}

func (TOMLSuite) TestEmptyPathError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	tomlObj := c.TOML().WithContents(dagger.TOML(`key = "value"`))

	_, err := tomlObj.Field([]string{}).AsString(ctx)
	require.Error(t, err)
}

func (TOMLSuite) TestFields(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	tomlObj := c.TOML().WithContents(dagger.TOML("name = \"Alice\"\nage = 25\nactive = true\n"))

	fields, err := tomlObj.Fields(ctx)
	require.NoError(t, err)
	require.Len(t, fields, 3)
	require.Contains(t, fields, "name")
	require.Contains(t, fields, "age")
	require.Contains(t, fields, "active")
}

func (TOMLSuite) TestWithField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	tomlObj := c.TOML()

	updatedObj := tomlObj.WithField([]string{"name"}, c.TOML().NewString("Bob"))
	nameValue, err := updatedObj.Field([]string{"name"}).AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "Bob", nameValue)

	finalObj := updatedObj.WithField([]string{"profile", "email"}, c.TOML().NewString("bob@example.com"))
	emailValue, err := finalObj.Field([]string{"profile", "email"}).AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "bob@example.com", emailValue)
}

func (TOMLSuite) TestContents(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	complexObj := c.TOML().
		WithField([]string{"name"}, c.TOML().NewString("Alice")).
		WithField([]string{"age"}, c.TOML().NewInteger(30)).
		WithField([]string{"active"}, c.TOML().NewBoolean(true))

	contents, err := complexObj.Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, string(contents), `name = "Alice"`)
	require.Contains(t, string(contents), "age = 30")
	require.Contains(t, string(contents), "active = true")
}

// TestWithFieldPreservesFormatting verifies that editing an existing document
// preserves its comments and surrounding formatting.
func (TOMLSuite) TestWithFieldPreservesFormatting(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	original := `# project config
title = "old"  # the title

[owner]
name = "Alice"
`
	contents, err := c.TOML().
		WithContents(dagger.TOML(original)).
		WithField([]string{"title"}, c.TOML().NewString("new")).
		Contents(ctx)
	require.NoError(t, err)

	got := string(contents)
	require.Contains(t, got, "# project config")
	require.Contains(t, got, "# the title")
	require.Contains(t, got, "[owner]")
	require.Contains(t, got, `name = "Alice"`)
	require.Contains(t, got, `title = "new"`)
	require.NotContains(t, got, `title = "old"`)
}

func (TOMLSuite) TestFileAsTOML(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("it converts toml file contents to TOML", func(ctx context.Context, t *testctx.T) {
		value, err := c.Directory().
			WithNewFile("test.toml", "somekey = \"somevalue\"\n").
			File("test.toml").
			AsTOML().
			Field([]string{"somekey"}).
			AsString(ctx)
		require.NoError(t, err)
		require.Equal(t, "somevalue", value)
	})

	t.Run("it returns error with non-toml", func(ctx context.Context, t *testctx.T) {
		_, err := c.Directory().
			WithNewFile("test.txt", "this is = = not toml").
			File("test.txt").
			AsTOML().
			Field([]string{"key"}).
			AsString(ctx)
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid TOML")
	})
}
