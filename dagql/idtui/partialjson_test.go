package idtui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPartialJSONFields(t *testing.T) {
	t.Run("complete object", func(t *testing.T) {
		fields := partialJSONFields(`{"path": "/foo/bar.go", "content": "hello world"}`)
		assert.Equal(t, map[string]string{
			"path":    "/foo/bar.go",
			"content": "hello world",
		}, fields)
	})

	t.Run("truncated mid-value", func(t *testing.T) {
		// Value for "content" is still streaming — included with partial content
		fields := partialJSONFields(`{"path": "/foo/bar.go", "content": "hello wor`)
		assert.Equal(t, map[string]string{
			"path":    "/foo/bar.go",
			"content": "hello wor",
		}, fields)
	})

	t.Run("truncated mid-value no closing quote", func(t *testing.T) {
		// Simulates streaming: {"command":"bash -c f
		fields := partialJSONFields(`{"command":"bash -c f`)
		assert.Equal(t, map[string]string{
			"command": "bash -c f",
		}, fields)
	})

	t.Run("truncated mid-value updates as more arrives", func(t *testing.T) {
		// Successive calls with more data should show progressively more
		fields1 := partialJSONFields(`{"command":"bash -c f`)
		assert.Equal(t, "bash -c f", fields1["command"])

		fields2 := partialJSONFields(`{"command":"bash -c foo --bar`)
		assert.Equal(t, "bash -c foo --bar", fields2["command"])

		fields3 := partialJSONFields(`{"command":"bash -c foo --bar"}`)
		assert.Equal(t, "bash -c foo --bar", fields3["command"])
	})

	t.Run("truncated mid-key", func(t *testing.T) {
		fields := partialJSONFields(`{"path": "/foo/bar.go", "cont`)
		assert.Equal(t, map[string]string{
			"path": "/foo/bar.go",
		}, fields)
	})

	t.Run("truncated after colon", func(t *testing.T) {
		fields := partialJSONFields(`{"path": "/foo/bar.go", "content": `)
		assert.Equal(t, map[string]string{
			"path": "/foo/bar.go",
		}, fields)
	})

	t.Run("empty object", func(t *testing.T) {
		fields := partialJSONFields(`{}`)
		assert.Equal(t, map[string]string{}, fields)
	})

	t.Run("empty input", func(t *testing.T) {
		fields := partialJSONFields(``)
		assert.Equal(t, map[string]string{}, fields)
	})

	t.Run("just opening brace", func(t *testing.T) {
		fields := partialJSONFields(`{`)
		assert.Equal(t, map[string]string{}, fields)
	})

	t.Run("non-string values are skipped", func(t *testing.T) {
		fields := partialJSONFields(`{"path": "/foo", "count": 42, "verbose": true, "desc": "hello"}`)
		assert.Equal(t, map[string]string{
			"path": "/foo",
			"desc": "hello",
		}, fields)
	})

	t.Run("escaped characters in strings", func(t *testing.T) {
		fields := partialJSONFields(`{"path": "foo\"bar", "desc": "line1\nline2"}`)
		assert.Equal(t, map[string]string{
			"path": `foo"bar`,
			"desc": "line1\nline2",
		}, fields)
	})

	t.Run("nested object value skipped", func(t *testing.T) {
		fields := partialJSONFields(`{"name": "test", "opts": {"a": 1}, "path": "/bar"}`)
		assert.Equal(t, map[string]string{
			"name": "test",
			"path": "/bar",
		}, fields)
	})

	t.Run("string array joined", func(t *testing.T) {
		fields := partialJSONFields(`{"name": "test", "args": ["a", "b"], "path": "/bar"}`)
		assert.Equal(t, map[string]string{
			"name": "test",
			"args": "a b",
			"path": "/bar",
		}, fields)
	})

	t.Run("truncated during nested object", func(t *testing.T) {
		fields := partialJSONFields(`{"name": "test", "opts": {"a": 1, "b`)
		assert.Equal(t, map[string]string{
			"name": "test",
		}, fields)
	})

	t.Run("null value skipped", func(t *testing.T) {
		fields := partialJSONFields(`{"path": "/foo", "x": null, "desc": "ok"}`)
		assert.Equal(t, map[string]string{
			"path": "/foo",
			"desc": "ok",
		}, fields)
	})

	t.Run("string array joined with spaces", func(t *testing.T) {
		fields := partialJSONFields(`{"include": ["test", "lint"], "path": "/foo"}`)
		assert.Equal(t, map[string]string{
			"include": "test lint",
			"path":    "/foo",
		}, fields)
	})

	t.Run("truncated string array excluded", func(t *testing.T) {
		fields := partialJSONFields(`{"include": ["test", "lin`)
		assert.Equal(t, map[string]string{}, fields)
	})

	t.Run("empty string array excluded", func(t *testing.T) {
		fields := partialJSONFields(`{"include": [], "path": "/foo"}`)
		assert.Equal(t, map[string]string{
			"path": "/foo",
		}, fields)
	})

	t.Run("mixed array skipped", func(t *testing.T) {
		fields := partialJSONFields(`{"items": [1, "a"], "path": "/foo"}`)
		assert.Equal(t, map[string]string{
			"path": "/foo",
		}, fields)
	})
}
