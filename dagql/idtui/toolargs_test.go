package idtui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToolArgStyle(t *testing.T) {
	// Case insensitive matching
	assert.Equal(t, argStylePath, toolArgStyle("Read", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("read", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("READ", "path"))

	// path variants
	assert.Equal(t, argStylePath, toolArgStyle("Read", "filePath"))
	assert.Equal(t, argStylePath, toolArgStyle("Read", "file_path"))
	assert.Equal(t, argStylePath, toolArgStyle("Write", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Edit", "filePath"))
	assert.Equal(t, argStylePath, toolArgStyle("Grep", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Find", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Ls", "path"))

	// Type_method matching: tries method part after _
	assert.Equal(t, argStyleContent, toolArgStyle("Container_withExec", "args"))
	assert.Equal(t, argStyleNone, toolArgStyle("Git_withCommit", "message")) // no "withcommit.message" rule
	// No rule for "file.path", so Directory_file doesn't match
	assert.Equal(t, argStyleNone, toolArgStyle("Directory_file", "path"))

	// Unknown tool: no special style for path
	assert.Equal(t, argStyleNone, toolArgStyle("SomeCustomTool", "path"))

	// description on declarative tools
	assert.Equal(t, argStyleDesc, toolArgStyle("DeclareOutput", "description"))
	assert.Equal(t, argStyleDesc, toolArgStyle("Save", "description"))
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

	// newText on Edit (oldText intentionally omitted)
	assert.Equal(t, argStyleContent, toolArgStyle("Edit", "newText"))
	assert.Equal(t, argStyleContent, toolArgStyle("Edit", "new_text"))

	// Grep.regex and Grep.pattern
	assert.Equal(t, argStyleDesc, toolArgStyle("Grep", "regex"))
	assert.Equal(t, argStyleDesc, toolArgStyle("Grep", "pattern"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "regex"))

	// Commit.message
	assert.Equal(t, argStyleDesc, toolArgStyle("Commit", "message"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "message"))

	// Checks.include
	assert.Equal(t, argStyleDesc, toolArgStyle("Checks", "include"))
	assert.Equal(t, argStyleDesc, toolArgStyle("Check", "include"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "include"))

	// isConventionalArg
	assert.True(t, isConventionalArg("Read", "path"))
	assert.True(t, isConventionalArg("Write", "content"))
	assert.True(t, isConventionalArg("anything", "prompt"))
	assert.False(t, isConventionalArg("Read", "limit"))
	assert.False(t, isConventionalArg("Read", "description"))
}
