package main

import (
	"fmt"
	"strings"
)

// parseWorkspaceTargetArgs parses optional explicit workspace syntax:
//
//	<workspace> -- <pattern...>
//	-- <pattern...>
//
// If no "--" separator is present, args are returned unchanged.
func parseWorkspaceTargetArgs(args []string, argsLenAtDash int) (*string, []string, error) {
	if argsLenAtDash < 0 {
		return nil, args, nil
	}
	if argsLenAtDash > len(args) {
		return nil, nil, fmt.Errorf("invalid argument separator index %d for %d args", argsLenAtDash, len(args))
	}

	prefix := args[:argsLenAtDash]
	suffix := args[argsLenAtDash:]

	switch len(prefix) {
	case 0:
		return nil, suffix, nil
	case 1:
		workspaceRef := prefix[0]
		return &workspaceRef, suffix, nil
	default:
		return nil, nil, fmt.Errorf("expected at most one workspace target before --, got %d", len(prefix))
	}
}

// parseWorkspaceTargetArgsWithImplicitWorkspace parses workspace target args
// and additionally supports workspace inference when no "--" separator is used.
//
// Inference only triggers when the first positional token looks like a
// workspace reference (path/git URL-like), keeping normal function/check names
// unchanged.
func parseWorkspaceTargetArgsWithImplicitWorkspace(args []string, argsLenAtDash int) (*string, []string, error) {
	workspaceRef, rest, err := parseWorkspaceTargetArgs(args, argsLenAtDash)
	if err != nil {
		return nil, nil, err
	}
	if workspaceRef != nil || argsLenAtDash >= 0 {
		return workspaceRef, rest, nil
	}
	if len(args) > 0 && isLikelyWorkspaceRef(args[0]) {
		workspace := args[0]
		return &workspace, args[1:], nil
	}
	return nil, args, nil
}

func parseChecksTargetArgs(args []string, argsLenAtDash int) (*string, []string, error) {
	return parseWorkspaceTargetArgsWithImplicitWorkspace(args, argsLenAtDash)
}

func parseGenerateTargetArgs(args []string, argsLenAtDash int) (*string, []string, error) {
	return parseWorkspaceTargetArgsWithImplicitWorkspace(args, argsLenAtDash)
}

func parseFunctionsTargetArgs(args []string, argsLenAtDash int) (*string, []string, error) {
	return parseWorkspaceTargetArgsWithImplicitWorkspace(args, argsLenAtDash)
}

func parseCallTargetArgs(args []string, argsLenAtDash int) (*string, []string, error) {
	return parseWorkspaceTargetArgsWithImplicitWorkspace(args, argsLenAtDash)
}

func isLikelyWorkspaceRef(arg string) bool {
	if arg == "" {
		return false
	}
	if strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") || strings.HasPrefix(arg, "/") {
		return true
	}
	if strings.Contains(arg, "://") || strings.HasPrefix(arg, "git@") {
		return true
	}
	// Check/generate pattern syntax is ":"-delimited, not "/"-delimited.
	// A slash in the first token is a strong signal this is a workspace ref.
	if strings.Contains(arg, "/") {
		return true
	}
	return false
}
