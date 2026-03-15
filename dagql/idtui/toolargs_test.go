package idtui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToolArgStyle(t *testing.T) {
	// path/filePath on file-oriented tools
	assert.Equal(t, argStylePath, toolArgStyle("Read", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("read", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Write", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Edit", "filePath"))
	assert.Equal(t, argStylePath, toolArgStyle("Grep", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Find", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Ls", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("read", "file_path"))

	// path on Dagger object methods containing "file" or "directory"
	assert.Equal(t, argStylePath, toolArgStyle("Directory_file", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Container_withFile", "path"))

	// path on unknown tools still gets path style (conservative default)
	assert.Equal(t, argStylePath, toolArgStyle("SomeCustomTool", "path"))

	// description on declarative tools
	assert.Equal(t, argStyleDesc, toolArgStyle("DeclareOutput", "description"))
	assert.Equal(t, argStyleDesc, toolArgStyle("Save", "description"))
	assert.Equal(t, argStyleDesc, toolArgStyle("CreateFoo", "description"))

	// description on non-declarative tools: no special style
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "description"))
	assert.Equal(t, argStyleNone, toolArgStyle("Container_withExec", "description"))

	// prompt: always content style
	assert.Equal(t, argStyleContent, toolArgStyle("anything", "prompt"))

	// command on exec-like tools
	assert.Equal(t, argStyleContent, toolArgStyle("Bash", "command"))
	assert.Equal(t, argStyleContent, toolArgStyle("Container_withExec", "command"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "command"))

	// content on write
	assert.Equal(t, argStyleContent, toolArgStyle("Write", "content"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "content"))

	// oldText/newText on edit
	assert.Equal(t, argStyleContent, toolArgStyle("Edit", "oldText"))
	assert.Equal(t, argStyleContent, toolArgStyle("Edit", "newText"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "oldText"))

	// isConventionalArg
	assert.True(t, isConventionalArg("Read", "path"))
	assert.True(t, isConventionalArg("Write", "content"))
	assert.False(t, isConventionalArg("Read", "limit"))
	assert.False(t, isConventionalArg("Read", "description"))
}
