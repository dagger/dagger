package core

// These tests cover TOML values, mirroring the JSON value tests. They verify
// scalar, table and array values, plus format-preserving edits of TOML
// documents.

import (
	"context"
	"fmt"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/internal/testutil"
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

// TestScalarContents verifies contents() of a scalar value returns a bare TOML
// value literal, mirroring JSONValue.contents which returns e.g. "5".
func (TOMLSuite) TestScalarContents(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	tomlContents, err := c.TOML().NewInteger(5).Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, dagger.TOML("5"), tomlContents)

	strContents, err := c.TOML().NewString("hello").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, dagger.TOML(`"hello"`), strContents)
}

// TestDatetimeTypePreserved verifies a TOML datetime stays a datetime (not a
// quoted string) when it flows through field() and withField().
func (TOMLSuite) TestDatetimeTypePreserved(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	doc := c.TOML().WithContents(dagger.TOML("created = 2024-01-02T03:04:05Z\n"))

	rebuilt, err := c.TOML().
		WithField([]string{"created"}, doc.Field([]string{"created"})).
		Contents(ctx)
	require.NoError(t, err)

	require.Contains(t, string(rebuilt), "created = 2024-01-02T03:04:05Z")
	require.NotContains(t, string(rebuilt), `created = "2024-01-02T03:04:05Z"`)
}

// TestSpecialFloatRoundTrip verifies TOML's special float values (inf / nan)
// load and round-trip.
func (TOMLSuite) TestSpecialFloatRoundTrip(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contents, err := c.TOML().WithContents(dagger.TOML("ratio = inf\n")).Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, string(contents), "ratio = inf")

	// And through a field round-trip, where the value model is re-encoded.
	doc := c.TOML().WithContents(dagger.TOML("ratio = inf\nneg = -inf\n"))
	rebuilt, err := c.TOML().
		WithField([]string{"ratio"}, doc.Field([]string{"ratio"})).
		WithField([]string{"neg"}, doc.Field([]string{"neg"})).
		Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, string(rebuilt), "ratio = inf")
	require.Contains(t, string(rebuilt), "neg = -inf")
}

// TestLargeIntegerPrecision verifies integers larger than 2^53 are not widened
// to float64 (TOML guarantees 64-bit integers).
func (TOMLSuite) TestLargeIntegerPrecision(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const big = 9007199254740993 // 2^53 + 1, not exactly representable as float64

	doc := c.TOML().WithContents(dagger.TOML(fmt.Sprintf("n = %d\n", big)))

	// The engine-side value model keeps the exact 64-bit integer: the literal
	// survives field() and a withField re-encode.
	contents, err := doc.Field([]string{"n"}).Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, dagger.TOML("9007199254740993"), contents)

	rebuilt, err := c.TOML().
		WithField([]string{"n"}, doc.Field([]string{"n"})).
		Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, string(rebuilt), "n = 9007199254740993")

	// asInteger also returns the exact value, observed via a raw GraphQL
	// query decoded into a typed int64. (The SDKs' generated bindings decode
	// response numbers into an untyped value first, widening them to float64
	// on the client side, so AsInteger through the high-level API is subject
	// to the client's number precision — same as JSONValue.asInteger.)
	res, err := testutil.QueryWithClient[struct {
		Toml struct {
			WithContents struct {
				Field struct {
					AsInteger int64
				}
			}
		}
	}](c, t, fmt.Sprintf(`{
		toml {
			withContents(contents: "n = %d\n") {
				field(path: ["n"]) {
					asInteger
				}
			}
		}
	}`, big), nil)
	require.NoError(t, err)
	require.Equal(t, int64(big), res.Toml.WithContents.Field.AsInteger)
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
