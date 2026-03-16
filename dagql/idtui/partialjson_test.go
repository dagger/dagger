package idtui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// pf is a shorthand for creating a complete parsedField in tests.
func pf(key, val string) parsedField { return parsedField{Key: key, Value: val, Complete: true} }

// partial is a shorthand for creating an incomplete parsedField in tests.
func partial(key, val string) parsedField { return parsedField{Key: key, Value: val, Complete: false} }

func TestPartialJSONFields(t *testing.T) {
	t.Run("complete object", func(t *testing.T) {
		fields := partialJSONFields(`{"path": "/foo/bar.go", "content": "hello world"}`)
		assert.Equal(t, []parsedField{
			pf("path", "/foo/bar.go"),
			pf("content", "hello world"),
		}, fields)
	})

	t.Run("truncated mid-value", func(t *testing.T) {
		fields := partialJSONFields(`{"path": "/foo/bar.go", "content": "hello wor`)
		assert.Equal(t, []parsedField{
			pf("path", "/foo/bar.go"),
			partial("content", "hello wor"),
		}, fields)
	})

	t.Run("truncated mid-value no closing quote", func(t *testing.T) {
		fields := partialJSONFields(`{"command":"bash -c f`)
		assert.Equal(t, []parsedField{
			partial("command", "bash -c f"),
		}, fields)
	})

	t.Run("truncated mid-value updates as more arrives", func(t *testing.T) {
		fields1 := partialJSONFields(`{"command":"bash -c f`)
		assert.Equal(t, partial("command", "bash -c f"), fields1[0])

		fields2 := partialJSONFields(`{"command":"bash -c foo --bar`)
		assert.Equal(t, partial("command", "bash -c foo --bar"), fields2[0])

		fields3 := partialJSONFields(`{"command":"bash -c foo --bar"}`)
		assert.Equal(t, pf("command", "bash -c foo --bar"), fields3[0])
	})

	t.Run("truncated mid-key", func(t *testing.T) {
		fields := partialJSONFields(`{"path": "/foo/bar.go", "cont`)
		assert.Equal(t, []parsedField{
			pf("path", "/foo/bar.go"),
		}, fields)
	})

	t.Run("truncated after colon", func(t *testing.T) {
		fields := partialJSONFields(`{"path": "/foo/bar.go", "content": `)
		assert.Equal(t, []parsedField{
			pf("path", "/foo/bar.go"),
		}, fields)
	})

	t.Run("empty object", func(t *testing.T) {
		fields := partialJSONFields(`{}`)
		assert.Empty(t, fields)
	})

	t.Run("empty input", func(t *testing.T) {
		fields := partialJSONFields(``)
		assert.Empty(t, fields)
	})

	t.Run("just opening brace", func(t *testing.T) {
		fields := partialJSONFields(`{`)
		assert.Empty(t, fields)
	})

	t.Run("non-string values are skipped", func(t *testing.T) {
		fields := partialJSONFields(`{"path": "/foo", "count": 42, "verbose": true, "desc": "hello"}`)
		assert.Equal(t, []parsedField{
			pf("path", "/foo"),
			pf("desc", "hello"),
		}, fields)
	})

	t.Run("escaped characters in strings", func(t *testing.T) {
		fields := partialJSONFields(`{"path": "foo\"bar", "desc": "line1\nline2"}`)
		assert.Equal(t, []parsedField{
			pf("path", `foo"bar`),
			pf("desc", "line1\nline2"),
		}, fields)
	})

	t.Run("nested object value skipped", func(t *testing.T) {
		fields := partialJSONFields(`{"name": "test", "opts": {"a": 1}, "path": "/bar"}`)
		assert.Equal(t, []parsedField{
			pf("name", "test"),
			pf("path", "/bar"),
		}, fields)
	})

	t.Run("string array joined", func(t *testing.T) {
		fields := partialJSONFields(`{"name": "test", "args": ["a", "b"], "path": "/bar"}`)
		assert.Equal(t, []parsedField{
			pf("name", "test"),
			pf("args", "a b"),
			pf("path", "/bar"),
		}, fields)
	})

	t.Run("truncated during nested object", func(t *testing.T) {
		fields := partialJSONFields(`{"name": "test", "opts": {"a": 1, "b`)
		assert.Equal(t, []parsedField{
			pf("name", "test"),
		}, fields)
	})

	t.Run("null value skipped", func(t *testing.T) {
		fields := partialJSONFields(`{"path": "/foo", "x": null, "desc": "ok"}`)
		assert.Equal(t, []parsedField{
			pf("path", "/foo"),
			pf("desc", "ok"),
		}, fields)
	})

	t.Run("string array joined with spaces", func(t *testing.T) {
		fields := partialJSONFields(`{"include": ["test", "lint"], "path": "/foo"}`)
		assert.Equal(t, []parsedField{
			pf("include", "test lint"),
			pf("path", "/foo"),
		}, fields)
	})

	t.Run("truncated string array excluded", func(t *testing.T) {
		fields := partialJSONFields(`{"include": ["test", "lin`)
		assert.Empty(t, fields)
	})

	t.Run("empty string array excluded", func(t *testing.T) {
		fields := partialJSONFields(`{"include": [], "path": "/foo"}`)
		assert.Equal(t, []parsedField{
			pf("path", "/foo"),
		}, fields)
	})

	t.Run("mixed array skipped", func(t *testing.T) {
		fields := partialJSONFields(`{"items": [1, "a"], "path": "/foo"}`)
		assert.Equal(t, []parsedField{
			pf("path", "/foo"),
		}, fields)
	})
}
