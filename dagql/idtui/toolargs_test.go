package idtui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToolArgStyle(t *testing.T) {
	// path/filePath on file-oriented tools
	assert.Equal(t, argStylePath, toolArgStyle("Read", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Read", "filePath"))
	assert.Equal(t, argStylePath, toolArgStyle("Read", "file_path"))
	assert.Equal(t, argStylePath, toolArgStyle("Write", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Edit", "filePath"))
	assert.Equal(t, argStylePath, toolArgStyle("Grep", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Find", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Ls", "path"))

	// path on unknown tools: no special style
	assert.Equal(t, argStyleNone, toolArgStyle("SomeCustomTool", "path"))

	// description on declarative tools
	assert.Equal(t, argStyleDesc, toolArgStyle("DeclareOutput", "description"))
	assert.Equal(t, argStyleDesc, toolArgStyle("Save", "description"))

	// description on non-declarative tools: no special style
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "description"))

	// prompt: always content style
	assert.Equal(t, argStyleContent, toolArgStyle("anything", "prompt"))
	assert.Equal(t, argStyleContent, toolArgStyle("Read", "prompt"))

	// command on Bash
	assert.Equal(t, argStyleContent, toolArgStyle("Bash", "command"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "command"))

	// content/contents on Write
	assert.Equal(t, argStyleContent, toolArgStyle("Write", "content"))
	assert.Equal(t, argStyleContent, toolArgStyle("Write", "contents"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "content"))

	// oldText/newText on Edit
	assert.Equal(t, argStyleContent, toolArgStyle("Edit", "oldText"))
	assert.Equal(t, argStyleContent, toolArgStyle("Edit", "newText"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "oldText"))

	// Grep.regex
	assert.Equal(t, argStyleDesc, toolArgStyle("Grep", "regex"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "regex"))

	// Commit.message
	assert.Equal(t, argStyleContent, toolArgStyle("Commit", "message"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "message"))

	// Checks.include
	assert.Equal(t, argStyleDesc, toolArgStyle("Checks", "include"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "include"))

	// isConventionalArg
	assert.True(t, isConventionalArg("Read", "path"))
	assert.True(t, isConventionalArg("Write", "content"))
	assert.True(t, isConventionalArg("anything", "prompt"))
	assert.False(t, isConventionalArg("Read", "limit"))
	assert.False(t, isConventionalArg("Read", "description"))
}
