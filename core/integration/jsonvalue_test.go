package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type JSONSuite struct{}

func TestJSON(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(JSONSuite{})
}

func (JSONSuite) TestInteger(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Test creating a JSON integer and retrieving its value
	jsonInt := c.JSON().NewInteger(42)

	// Test AsInteger method
	value, err := jsonInt.AsInteger(ctx)
	require.NoError(t, err)
	require.Equal(t, 42, value)

	// Test with negative integer
	jsonNegInt := c.JSON().NewInteger(-123)
	negValue, err := jsonNegInt.AsInteger(ctx)
	require.NoError(t, err)
	require.Equal(t, -123, negValue)

	// Test with zero
	jsonZero := c.JSON().NewInteger(0)
	zeroValue, err := jsonZero.AsInteger(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, zeroValue)
}

func (JSONSuite) TestString(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Test creating a JSON string and retrieving its value
	jsonStr := c.JSON().NewString("hello world")
	value, err := jsonStr.AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello world", value)

	// Test with empty string
	jsonEmpty := c.JSON().NewString("")
	emptyValue, err := jsonEmpty.AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "", emptyValue)

	// Test with special characters
	jsonSpecial := c.JSON().NewString("hello\nworld\t\"quotes\"")
	specialValue, err := jsonSpecial.AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello\nworld\t\"quotes\"", specialValue)
}

func (JSONSuite) TestBoolean(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Test creating a JSON boolean true and retrieving its value
	jsonTrue := c.JSON().NewBoolean(true)
	trueValue, err := jsonTrue.AsBoolean(ctx)
	require.NoError(t, err)
	require.Equal(t, true, trueValue)

	// Test creating a JSON boolean false and retrieving its value
	jsonFalse := c.JSON().NewBoolean(false)
	falseValue, err := jsonFalse.AsBoolean(ctx)
	require.NoError(t, err)
	require.Equal(t, false, falseValue)
}

func (JSONSuite) TestArray(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Create a JSON array using WithContents
	jsonArrayBytes := `[1, "hello", true, null]`
	jsonArray := c.JSON().WithContents(dagger.JSON(jsonArrayBytes))

	// Test AsArray method
	arrayValues, err := jsonArray.AsArray(ctx)
	require.NoError(t, err)
	require.Len(t, arrayValues, 4)

	// Test first element (integer)
	firstValue, err := arrayValues[0].AsInteger(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, firstValue)

	// Test second element (string)
	secondValue, err := arrayValues[1].AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", secondValue)

	// Test third element (boolean)
	thirdValue, err := arrayValues[2].AsBoolean(ctx)
	require.NoError(t, err)
	require.Equal(t, true, thirdValue)
}

func (JSONSuite) TestNestedPaths(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Create a nested JSON object
	nestedJSON := `{"user": {"name": "John", "age": 30, "profile": {"email": "john@example.com", "active": true}}}`
	jsonObj := c.JSON().WithContents(dagger.JSON(nestedJSON))

	// Test accessing nested string field
	nameField := jsonObj.Field([]string{"user", "name"})
	nameValue, err := nameField.AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "John", nameValue)

	// Test accessing nested integer field
	ageField := jsonObj.Field([]string{"user", "age"})
	ageValue, err := ageField.AsInteger(ctx)
	require.NoError(t, err)
	require.Equal(t, 30, ageValue)

	// Test accessing deeply nested string field
	emailField := jsonObj.Field([]string{"user", "profile", "email"})
	emailValue, err := emailField.AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "john@example.com", emailValue)

	// Test accessing deeply nested boolean field
	activeField := jsonObj.Field([]string{"user", "profile", "active"})
	activeValue, err := activeField.AsBoolean(ctx)
	require.NoError(t, err)
	require.Equal(t, true, activeValue)
}

func (JSONSuite) TestEmptyPathError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Create a JSON object
	jsonObj := c.JSON().WithContents(`{"key": "value"}`)

	// Test that accessing with empty path returns an error
	_, err := jsonObj.Field([]string{}).AsString(ctx)
	require.Error(t, err)
}

func (JSONSuite) TestFields(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Create a JSON object
	jsonObj := c.JSON().WithContents(`{"name": "Alice", "age": 25, "active": true}`)

	// Test fields method
	fields, err := jsonObj.Fields(ctx)
	require.NoError(t, err)
	require.Len(t, fields, 3)

	// Convert to strings for comparison
	fieldNames := make([]string, len(fields))
	copy(fieldNames, fields)
	require.Contains(t, fieldNames, "name")
	require.Contains(t, fieldNames, "age")
	require.Contains(t, fieldNames, "active")
}

func (JSONSuite) TestWithField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Start with an empty JSON object
	jsonObj := c.JSON()

	// Add a field
	updatedObj := jsonObj.WithField([]string{"name"}, c.JSON().NewString("Bob"))

	// Verify the field was added
	nameField := updatedObj.Field([]string{"name"})
	nameValue, err := nameField.AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "Bob", nameValue)

	// Add a nested field
	finalObj := updatedObj.WithField([]string{"profile", "email"}, c.JSON().NewString("bob@example.com"))

	// Verify the nested field was added
	emailField := finalObj.Field([]string{"profile", "email"})
	emailValue, err := emailField.AsString(ctx)
	require.NoError(t, err)
	require.Equal(t, "bob@example.com", emailValue)
}

func (JSONSuite) TestBytes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Create a complex JSON object
	complexObj := c.JSON().
		WithField([]string{"name"}, c.JSON().NewString("Alice")).
		WithField([]string{"age"}, c.JSON().NewInteger(30)).
		WithField([]string{"active"}, c.JSON().NewBoolean(true))

	// Test normal bytes output
	bytes, err := complexObj.Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, string(bytes), "Alice")
	require.Contains(t, string(bytes), "30")
	require.Contains(t, string(bytes), "true")

	// Test pretty-printed bytes output
	prettyBytes, err := complexObj.Contents(ctx, dagger.JSONValueContentsOpts{Pretty: true})
	require.NoError(t, err)
	prettyStr := string(prettyBytes)
	require.Contains(t, prettyStr, "Alice")
	require.Contains(t, prettyStr, "30")
	require.Contains(t, prettyStr, "true")
	// Pretty printed should contain newlines and indentation
	require.Contains(t, prettyStr, "\n")
	require.Contains(t, prettyStr, "  ") // indentation
}
