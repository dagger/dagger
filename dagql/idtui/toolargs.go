package idtui

// argStyle describes how a tool argument should be rendered in the TUI.
type argStyle int

const (
	// argStyleNone means no special rendering; shown in the fallback JSON line.
	argStyleNone argStyle = iota
	// argStylePath means the value is a file path shown in cyan in the span header.
	argStylePath
	// argStyleDesc means the value is a short description shown faint in the span header.
	argStyleDesc
	// argStyleContent means the value is content (e.g. a prompt) rendered as a
	// truncated italic line beneath the span header.
	argStyleContent
)

// toolArgStyles maps "ToolName.argName" to a rendering style.
// The tool name match is case-sensitive and must match the LLMTool
// span attribute exactly.
var toolArgStyles = map[string]argStyle{
	// File-oriented tools: path in cyan
	"Read.path":           argStylePath,
	"Read.filePath":       argStylePath,
	"Read.file_path":      argStylePath,
	"Write.path":          argStylePath,
	"Write.filePath":      argStylePath,
	"Write.file_path":     argStylePath,
	"Edit.path":           argStylePath,
	"Edit.filePath":       argStylePath,
	"Edit.file_path":      argStylePath,
	"Grep.path":           argStylePath,
	"Find.path":           argStylePath,
	"Ls.path":             argStylePath,

	// Write content
	"Write.content":  argStyleContent,
	"Write.contents": argStyleContent,

	// Edit payloads
	"Edit.oldText":  argStyleContent,
	"Edit.old_text": argStyleContent,
	"Edit.newText":  argStyleContent,
	"Edit.new_text": argStyleContent,

	// Shell commands
	"Bash.command": argStyleContent,

	// Grep pattern
	"Grep.regex": argStyleDesc,

	// Declarative tools
	"DeclareOutput.description": argStyleDesc,
	"Save.description":          argStyleDesc,

	// Commit
	"Commit.message": argStyleContent,

	// Check/test tools
	"Checks.include": argStyleDesc,

	// Prompt is content for any tool — see toolArgStyle fallback
}

// toolArgStyle returns the rendering style for a given (toolName, argName) pair.
func toolArgStyle(toolName, argName string) argStyle {
	if style, ok := toolArgStyles[toolName+"."+argName]; ok {
		return style
	}
	// Fallback: prompt is always content-style regardless of tool.
	if argName == "prompt" {
		return argStyleContent
	}
	return argStyleNone
}

// isConventionalArg returns true if the arg has any special rendering style
// for the given tool, meaning it should be filtered from the fallback JSON line.
func isConventionalArg(toolName, argName string) bool {
	return toolArgStyle(toolName, argName) != argStyleNone
}
