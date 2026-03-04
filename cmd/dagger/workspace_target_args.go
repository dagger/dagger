package main

import "fmt"

// parseWorkspaceTargetArgs parses optional explicit workspace syntax:
//
//	<workspace> -- <pattern...>
//	-- <pattern...>
//
// If no "--" separator is present, args are returned unchanged as patterns.
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
