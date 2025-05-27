package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"

	"github.com/dagger/testctx"
)

type JSONValueSuite struct{}

func TestJSONValue(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(JSONValueSuite{})
}

func (JSONValueSuite) TestNewJSONValue(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("create empty json", func(ctx context.Context, t *testctx.T) {
		json := c.JSON()

		// Get the string representation
		result, err := json.Get(ctx, "")
		require.NoError(t, err)
		require.Equal(t, "null", string(result))
	})
}

func (JSONValueSuite) TestSetAndGetString(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("set and get string at root", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetString("", "hello world")

		value, err := json.GetString(ctx, "")
		require.NoError(t, err)
		require.Equal(t, "hello world", value)
	})

	t.Run("set and get string at path", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetString("name", "Alice")

		value, err := json.GetString(ctx, "name")
		require.NoError(t, err)
		require.Equal(t, "Alice", value)
	})

	t.Run("set and get string at nested path", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetString("user.profile.name", "Bob")

		value, err := json.GetString(ctx, "user.profile.name")
		require.NoError(t, err)
		require.Equal(t, "Bob", value)
	})

	t.Run("get string from non-string value should error", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetInteger("count", 42)

		_, err := json.GetString(ctx, "count")
		require.Error(t, err)
		require.Contains(t, err.Error(), "not a string")
	})
}

func (JSONValueSuite) TestSetAndGetInteger(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("set and get integer", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetInteger("count", 42)

		value, err := json.GetInt(ctx, "count")
		require.NoError(t, err)
		require.Equal(t, 42, value)
	})

	t.Run("set and get integer at nested path", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetInteger("metrics.requests.count", 1337)

		value, err := json.GetInt(ctx, "metrics.requests.count")
		require.NoError(t, err)
		require.Equal(t, 1337, value)
	})

	t.Run("set negative integer", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetInteger("negative", -100)

		value, err := json.GetInt(ctx, "negative")
		require.NoError(t, err)
		require.Equal(t, -100, value)
	})

	t.Run("set zero", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetInteger("zero", 0)

		value, err := json.GetInt(ctx, "zero")
		require.NoError(t, err)
		require.Equal(t, 0, value)
	})

	t.Run("get integer from non-number value should error", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetString("name", "Alice")

		_, err := json.GetInt(ctx, "name")
		require.Error(t, err)
		require.Contains(t, err.Error(), "not a number")
	})
}

func (JSONValueSuite) TestSetAndGetBoolean(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("set and get boolean true", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetBoolean("enabled", true)

		value, err := json.GetBool(ctx, "enabled")
		require.NoError(t, err)
		require.Equal(t, true, value)
	})

	t.Run("set and get boolean false", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetBoolean("disabled", false)

		value, err := json.GetBool(ctx, "disabled")
		require.NoError(t, err)
		require.Equal(t, false, value)
	})

	t.Run("set boolean at nested path", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetBoolean("feature.toggles.newUI", true)

		value, err := json.GetBool(ctx, "feature.toggles.newUI")
		require.NoError(t, err)
		require.Equal(t, true, value)
	})

	t.Run("get boolean from non-boolean value should error", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetString("name", "Alice")

		_, err := json.GetBool(ctx, "name")
		require.Error(t, err)
		require.Contains(t, err.Error(), "not a boolean")
	})
}

func (JSONValueSuite) TestSetAndGetJSON(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("set and get complex JSON object", func(ctx context.Context, t *testctx.T) {
		complexJSON := dagger.JSON(`{"users": [{"name": "Alice", "age": 30}, {"name": "Bob", "age": 25}], "total": 2}`)
		json := c.JSON().SetJSON("data", complexJSON)

		value, err := json.GetJSON(ctx, "data")
		require.NoError(t, err)
		require.JSONEq(t, string(complexJSON), string(value))
	})

	t.Run("set invalid JSON should error", func(ctx context.Context, t *testctx.T) {
		invalidJSON := dagger.JSON(`{"invalid": json}`)
		json := c.JSON().SetJSON("data", invalidJSON)

		_, err := json.Get(ctx, "data")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal JSON")
	})

	t.Run("set and get JSON array", func(ctx context.Context, t *testctx.T) {
		arrayJSON := dagger.JSON(`[1, 2, 3, "four", true]`)
		json := c.JSON().SetJSON("items", arrayJSON)

		value, err := json.GetJSON(ctx, "items")
		require.NoError(t, err)
		require.JSONEq(t, string(arrayJSON), string(value))
	})

	t.Run("set null JSON", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetJSON("nullable", "null")

		value, err := json.GetJSON(ctx, "nullable")
		require.NoError(t, err)
		require.Equal(t, "null", string(value))
	})
}

func (JSONValueSuite) TestGet(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("get whole JSON object", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().
			SetString("name", "Alice").
			SetInteger("age", 30).
			SetBoolean("active", true)

		value, err := json.Get(ctx, "")
		require.NoError(t, err)
		require.JSONEq(t, `{"name": "Alice", "age": 30, "active": true}`, string(value))
	})

	t.Run("get with dot path", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().
			SetString("user.profile.name", "Alice").
			SetInteger("user.profile.age", 30)

		value, err := json.Get(ctx, ".")
		require.NoError(t, err)
		require.JSONEq(t, `{"user": {"profile": {"name": "Alice", "age": 30}}}`, string(value))
	})

	t.Run("get non-existent path should error", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetString("name", "Alice")

		_, err := json.Get(ctx, "nonexistent")
		require.Error(t, err)
		require.Contains(t, err.Error(), "path not found")
	})
}

func (JSONValueSuite) TestUnset(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("unset simple property", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().
			SetString("name", "Alice").
			SetInteger("age", 30).
			Unset("name")

		// Should only have age now
		value, err := json.Get(ctx, "")
		require.NoError(t, err)
		require.JSONEq(t, `{"age": 30}`, string(value))

		// Getting the unset property should error
		_, err = json.GetString(ctx, "name")
		require.Error(t, err)
		require.Contains(t, err.Error(), "path not found")
	})

	t.Run("unset nested property", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().
			SetString("user.profile.name", "Alice").
			SetInteger("user.profile.age", 30).
			SetString("user.email", "alice@example.com").
			Unset("user.profile.name")

		// Should still have age and email
		value, err := json.Get(ctx, "")
		require.NoError(t, err)
		require.JSONEq(t, `{"user": {"profile": {"age": 30}, "email": "alice@example.com"}}`, string(value))
	})

	t.Run("unset whole object with empty path", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().
			SetString("name", "Alice").
			SetInteger("age", 30).
			Unset("")

		value, err := json.Get(ctx, "")
		require.NoError(t, err)
		require.Equal(t, "null", string(value))
	})

	t.Run("unset with dot path", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().
			SetString("name", "Alice").
			SetInteger("age", 30).
			Unset(".")

		value, err := json.Get(ctx, "")
		require.NoError(t, err)
		require.Equal(t, "null", string(value))
	})

	t.Run("unset non-existent path should not error", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetString("name", "Alice")

		// This should not error, just be a no-op
		json = json.Unset("nonexistent")

		value, err := json.Get(ctx, "")
		require.NoError(t, err)
		require.JSONEq(t, `{"name": "Alice"}`, string(value))
	})
}

func (JSONValueSuite) TestComplexOperations(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("build complex nested structure", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().
			SetString("application.name", "MyApp").
			SetString("application.version", "1.0.0").
			SetInteger("application.port", 8080).
			SetBoolean("application.debug", true).
			SetString("database.host", "localhost").
			SetInteger("database.port", 5432).
			SetString("database.name", "mydb").
			SetJSON("features", `["auth", "logging", "metrics"]`)

		expected := `{
			"application": {
				"name": "MyApp",
				"version": "1.0.0",
				"port": 8080,
				"debug": true
			},
			"database": {
				"host": "localhost",
				"port": 5432,
				"name": "mydb"
			},
			"features": ["auth", "logging", "metrics"]
		}`

		value, err := json.Get(ctx, "")
		require.NoError(t, err)
		require.JSONEq(t, expected, string(value))
	})

	t.Run("overwrite existing values", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().
			SetString("name", "Alice").
			SetInteger("age", 30).
			SetString("name", "Bob"). // Overwrite string
			SetInteger("age", 25)     // Overwrite integer

		value, err := json.Get(ctx, "")
		require.NoError(t, err)
		require.JSONEq(t, `{"name": "Bob", "age": 25}`, string(value))
	})

	t.Run("replace object with primitive", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().
			SetString("user.name", "Alice").
			SetInteger("user.age", 30).
			SetString("user", "Just a string now") // Replace whole object

		value, err := json.Get(ctx, "")
		require.NoError(t, err)
		require.JSONEq(t, `{"user": "Just a string now"}`, string(value))
	})

	t.Run("chain multiple operations", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().
			SetString("temp.name", "temp").
			SetInteger("count", 1).
			Unset("temp").
			SetBoolean("active", true).
			SetJSON("config", `{"key": "value"}`)

		value, err := json.Get(ctx, "")
		require.NoError(t, err)
		require.JSONEq(t, `{"count": 1, "active": true, "config": {"key": "value"}}`, string(value))
	})
}

func (JSONValueSuite) TestErrorCases(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("get from empty JSON", func(ctx context.Context, t *testctx.T) {
		json := c.JSON()

		_, err := json.GetString(ctx, "name")
		require.Error(t, err)
		require.Contains(t, err.Error(), "path not found")
	})

	t.Run("get nested path from primitive value", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetString("", "just a string")

		_, err := json.GetString(ctx, "nested.path")
		require.Error(t, err)
		require.Contains(t, err.Error(), "path not found")
	})

	t.Run("type mismatch errors", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().
			SetString("text", "hello").
			SetInteger("number", 42).
			SetBoolean("flag", true)

		// Try to get string as int
		_, err := json.GetInt(ctx, "text")
		require.Error(t, err)
		require.Contains(t, err.Error(), "not a number")

		// Try to get int as boolean
		_, err = json.GetBool(ctx, "number")
		require.Error(t, err)
		require.Contains(t, err.Error(), "not a boolean")

		// Try to get boolean as string
		_, err = json.GetString(ctx, "flag")
		require.Error(t, err)
		require.Contains(t, err.Error(), "not a string")
	})
}

func (JSONValueSuite) TestEmptyAndNullHandling(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("empty string path handling", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetString("", "root value")

		value, err := json.GetString(ctx, "")
		require.NoError(t, err)
		require.Equal(t, "root value", value)
	})

	t.Run("dot path equivalent to empty path", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetInteger(".", 42)

		value, err := json.GetInt(ctx, ".")
		require.NoError(t, err)
		require.Equal(t, 42, value)

		// Should be the same as empty path
		valueEmpty, err := json.GetInt(ctx, "")
		require.NoError(t, err)
		require.Equal(t, value, valueEmpty)
	})

	t.Run("null value handling", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().SetJSON("nullable", "null")

		value, err := json.GetJSON(ctx, "nullable")
		require.NoError(t, err)
		require.Equal(t, "null", string(value))
	})
}

func (JSONValueSuite) TestChaining(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("method chaining works", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().
			SetString("a", "value_a").
			SetInteger("b", 123).
			SetBoolean("c", true).
			SetJSON("d", `{"nested": "object"}`).
			Unset("b").
			SetString("e", "value_e")

		// Check final state
		a, err := json.GetString(ctx, "a")
		require.NoError(t, err)
		require.Equal(t, "value_a", a)

		_, err = json.GetInt(ctx, "b")
		require.Error(t, err) // Should be unset

		cVal, err := json.GetBool(ctx, "c")
		require.NoError(t, err)
		require.Equal(t, true, cVal)

		d, err := json.GetJSON(ctx, "d")
		require.NoError(t, err)
		require.JSONEq(t, `{"nested": "object"}`, string(d))

		e, err := json.GetString(ctx, "e")
		require.NoError(t, err)
		require.Equal(t, "value_e", e)
	})
}

func (JSONValueSuite) TestMixedTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("different types in same structure", func(ctx context.Context, t *testctx.T) {
		json := c.JSON().
			SetString("user.name", "Alice").
			SetInteger("user.age", 30).
			SetBoolean("user.active", true).
			SetJSON("user.preferences", `{"theme": "dark", "notifications": false}`).
			SetString("user.email", "alice@example.com")

		// Verify each type can be retrieved correctly
		name, err := json.GetString(ctx, "user.name")
		require.NoError(t, err)
		require.Equal(t, "Alice", name)

		age, err := json.GetInt(ctx, "user.age")
		require.NoError(t, err)
		require.Equal(t, 30, age)

		active, err := json.GetBool(ctx, "user.active")
		require.NoError(t, err)
		require.Equal(t, true, active)

		prefs, err := json.GetJSON(ctx, "user.preferences")
		require.NoError(t, err)
		require.JSONEq(t, `{"theme": "dark", "notifications": false}`, string(prefs))

		email, err := json.GetString(ctx, "user.email")
		require.NoError(t, err)
		require.Equal(t, "alice@example.com", email)

		// Get the entire user object
		user, err := json.GetJSON(ctx, "user")
		require.NoError(t, err)
		require.Contains(t, user, "Alice")
		require.Contains(t, user, "30")
		require.Contains(t, user, "true")
		require.Contains(t, user, "dark")
	})
}
