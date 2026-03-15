package idtui

import "strings"

// argStyle describes how a tool argument should be rendered in the TUI.
type argStyle int

const (
	// argStyleNone means no special rendering; shown in the fallback JSON line.
	argStyleNone argStyle = iota
	// argStylePath means the value is a file path shown in cyan in the span header.
	argStylePath
	// argStyleDesc means the value is a description shown faint in the span header.
	argStyleDesc
	// argStyleContent means the value is content (e.g. a prompt) rendered as a
	// truncated italic line beneath the span header.
	argStyleContent
)

// toolArgStyle returns the rendering style for a given (toolName, argName) pair.
// This uses a combination of tool name pattern + argument name to decide how
// to render, avoiding false positives from arg names that mean different things
// on different tools.
func toolArgStyle(toolName, argName string) argStyle {
	lower := strings.ToLower(toolName)

	switch argName {
	case "path", "filePath", "file_path":
		// File paths: show in cyan for file-oriented tools.
		// Match tools like Read, Write, Edit, Grep, Find, Ls,
		// as well as Dagger object methods like Directory_file,
		// Container_withFile, etc.
		switch {
		case lower == "read" || lower == "write" || lower == "edit":
			return argStylePath
		case lower == "grep" || lower == "find" || lower == "ls":
			return argStylePath
		case strings.Contains(lower, "file") ||
			strings.Contains(lower, "directory") ||
			strings.Contains(lower, "path"):
			return argStylePath
		default:
			// For any other tool, path/filePath is still likely
			// meaningful enough to highlight.
			return argStylePath
		}

	case "description":
		// Description: show faint in header for declarative tools.
		switch {
		case lower == "declareoutput" || lower == "save":
			return argStyleDesc
		case strings.Contains(lower, "declare") ||
			strings.Contains(lower, "create") ||
			strings.Contains(lower, "register"):
			return argStyleDesc
		default:
			return argStyleNone
		}

	case "prompt":
		// Prompt: show as content line for LLM-related tools.
		return argStyleContent

	case "command":
		// Shell commands: show in header for bash-like tools.
		switch {
		case lower == "bash" || lower == "exec" ||
			strings.Contains(lower, "exec") ||
			strings.Contains(lower, "shell"):
			return argStyleContent
		default:
			return argStyleNone
		}

	case "content":
		// File content: show as content preview for write-like tools.
		switch {
		case lower == "write":
			return argStyleContent
		default:
			return argStyleNone
		}

	case "oldText", "old_text", "newText", "new_text":
		// Edit payloads: show as content preview for edit-like tools.
		if lower == "edit" {
			return argStyleContent
		}
		return argStyleNone

	default:
		return argStyleNone
	}
}

// isConventionalArg returns true if the arg has any special rendering style
// for the given tool, meaning it should be filtered from the fallback JSON line.
func isConventionalArg(toolName, argName string) bool {
	return toolArgStyle(toolName, argName) != argStyleNone
}
