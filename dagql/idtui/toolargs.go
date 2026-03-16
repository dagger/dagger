package idtui

import "strings"

// argStyle describes how a tool argument should be rendered in the TUI.
type argStyle int

const (
	// argStyleNone means no special rendering; shown in the fallback JSON line.
	argStyleNone argStyle = iota
	// argStylePath means the value is a file path shown in cyan in the span header.
	argStylePath
	// argStyleDesc means the value is a description shown faint in the span header,
	// truncated to the first line.
	argStyleDesc
	// argStyleContent means the value is content (e.g. a prompt) shown as a
	// truncated faint italic string in the span header.
	argStyleContent
)

// toolArgStyles maps "toolname.argname" (lowercase) to a rendering style.
// Tool names are lowercased for matching, so "Read", "read", and
// "myserver_read" (after prefix stripping) all match "read".
//
// For Dagger object method tools like "Container_withExec", the lookup
// tries both the full name and the method part after "_".
var toolArgStyles = map[string]argStyle{
	// File-oriented tools: path in cyan
	"read.path":       argStylePath,
	"read.filepath":   argStylePath,
	"read.file_path":  argStylePath,
	"write.path":      argStylePath,
	"write.filepath":  argStylePath,
	"write.file_path": argStylePath,
	"edit.path":       argStylePath,
	"edit.filepath":   argStylePath,
	"edit.file_path":  argStylePath,
	"grep.path":       argStylePath,
	"find.path":       argStylePath,
	"ls.path":         argStylePath,

	// Write content
	"write.content":  argStyleContent,
	"write.contents": argStyleContent,

	// Edit payloads
	"edit.oldtext":  argStyleContent,
	"edit.old_text": argStyleContent,
	"edit.newtext":  argStyleContent,
	"edit.new_text": argStyleContent,

	// Shell commands
	"bash.command":  argStyleContent,
	"withexec.args": argStyleContent,

	// Grep pattern
	"grep.regex":   argStyleDesc,
	"grep.pattern": argStyleDesc,

	// Declarative tools
	"declareoutput.description": argStyleDesc,
	"save.description":          argStyleDesc,

	// Commit
	"commit.message": argStyleDesc,

	// Check/test tools
	"checks.include": argStyleDesc,
	"check.include":  argStyleDesc,

	// Sub-agents
	"research.description":   argStyleDesc,
	"research.prompt":        argStyleContent,
	"rabbithole.description": argStyleDesc,
	"rabbithole.prompt":      argStyleContent,
}

// toolArgStyle returns the rendering style for a given (toolName, argName) pair.
// The lookup is case-insensitive. For "Type_method" tool names, it tries
// both the full name and just the method part.
func toolArgStyle(toolName, argName string) argStyle {
	lower := strings.ToLower(toolName)
	argLower := strings.ToLower(argName)

	// Try full tool name first
	if style, ok := toolArgStyles[lower+"."+argLower]; ok {
		return style
	}

	// Try method part only (e.g. "container_withexec" → "withexec")
	if idx := strings.LastIndex(lower, "_"); idx >= 0 {
		method := lower[idx+1:]
		if style, ok := toolArgStyles[method+"."+argLower]; ok {
			return style
		}
	}

	// Fallback: prompt is always content-style regardless of tool.
	if argLower == "prompt" {
		return argStyleContent
	}

	return argStyleNone
}

// isConventionalArg returns true if the arg has any special rendering style
// for the given tool, meaning it should be filtered from the fallback JSON line.
func isConventionalArg(toolName, argName string) bool {
	return toolArgStyle(toolName, argName) != argStyleNone
}
