package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"dagger.io/dagger"
)

// This is a helper executable that bridges Dagger and Nushell
// It gets function call context from Dagger, executes Nushell, and returns results

// isDaggerObjectID checks if a string is a Dagger object ID
// Dagger object IDs have the format "TypeName:id..." where TypeName is like Container, Directory, File, etc.
func isDaggerObjectID(s string) bool {
	// Dagger object IDs can be in two formats:
	// 1. Simple format: "TypeName:id..." (e.g., "Container:abc123")
	// 2. Protobuf format: Long base64-encoded strings (usually 100+ chars)

	// Check for simple type prefix format
	daggerTypes := []string{
		"Container:", "Directory:", "File:", "Secret:",
		"Service:", "CacheVolume:", "Socket:", "Platform:",
	}
	for _, prefix := range daggerTypes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}

	// Check for protobuf format: long base64-like strings
	// These are typically 100+ characters
	// Base64 strings can end with =, ==, or no padding
	if len(s) > 100 {
		return true
	}

	return false
}

func main() {
	os.Exit(run())
}

func run() int {
	ctx := context.Background()

	// Connect to Dagger
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to Dagger: %v\n", err)
		return 1
	}
	defer client.Close()

	// Get current function call
	fnCall := client.CurrentFunctionCall()

	// Get function name
	fnName, err := fnCall.Name(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get function name: %v\n", err)
		return 1
	}

	// Get parent name (object name)
	parentName, err := fnCall.ParentName(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get parent name: %v\n", err)
		return 1
	}

	// Get input arguments
	inputArgs, err := fnCall.InputArgs(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get input args: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "Executing function: %s.%s\n", parentName, fnName)

	// Build Nushell command with positional arguments
	// NOTE: Dagger passes arguments in alphabetical order by parameter name.
	// Nushell functions must define parameters in the same alphabetical order.
	var args []string

	for _, arg := range inputArgs {
		argName, _ := arg.Name(ctx)
		argValue, _ := arg.Value(ctx)

		// Build argument string for Nushell
		// Parse the JSON value and format it appropriately
		var val interface{}
		if err := json.Unmarshal([]byte(string(argValue)), &val); err == nil {
			switch v := val.(type) {
			case string:
				// Check if this is a Dagger object ID (format: "TypeName:id...")
				// Wrap it in a Nushell record: {id: "..."}
				if isDaggerObjectID(v) {
					args = append(args, fmt.Sprintf(`{id: "%s"}`, v))
				} else {
					// Regular strings get quoted
					args = append(args, fmt.Sprintf(`"%s"`, v))
				}
			case float64:
				args = append(args, fmt.Sprintf(`%v`, v))
			case bool:
				args = append(args, fmt.Sprintf(`%v`, v))
			case []interface{}:
				// Arrays/lists - pass as Nushell list syntax
				// Convert to JSON and pass without quotes so Nushell can parse it
				args = append(args, string(argValue))
			case map[string]interface{}:
				// Objects/records - pass as Nushell record syntax
				args = append(args, string(argValue))
			default:
				// For other complex types, pass the raw JSON
				args = append(args, fmt.Sprintf(`'%s'`, argValue))
			}
		} else {
			// If JSON parsing fails, pass as-is
			fmt.Fprintf(os.Stderr, "Warning: Failed to parse argument %s: %v\n", argName, err)
			args = append(args, fmt.Sprintf(`'%s'`, argValue))
		}

		// Log the argument for debugging
		fmt.Fprintf(os.Stderr, "Argument %s: %s\n", argName, args[len(args)-1])
	}

	// Construct Nushell command
	// Pipe output through 'to json' to ensure we get machine-readable output
	nuCommand := fmt.Sprintf(`use /src/main.nu *; %s %s | to json`, fnName, strings.Join(args, " "))

	fmt.Fprintf(os.Stderr, "Running: nu -c '%s'\n", nuCommand)

	// Execute Nushell
	cmd := exec.Command("nu", "-c", nuCommand)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			fmt.Fprintf(os.Stderr, "Nushell error: %s\n", exitErr.Stderr)
		}
		fmt.Fprintf(os.Stderr, "Failed to execute Nushell: %v\n", err)
		return 1
	}

	// The output should be the function result
	result := strings.TrimSpace(string(output))

	fmt.Fprintf(os.Stderr, "Result: %s\n", result)

	// Return result as JSON
	// Dagger expects different formats depending on the return type:
	// - For primitive types (string, int, bool): marshal the value directly
	// - For Dagger objects (Container, Directory, etc.): extract the ID from the record
	//
	// Nushell functions now return records for Dagger objects: {id: "TypeName:..."}
	// We need to extract just the ID string before returning to Dagger
	var resultValue interface{}

	// Try to parse as JSON first
	if err := json.Unmarshal([]byte(result), &resultValue); err != nil {
		// Not valid JSON, treat as string
		resultValue = result
	} else {
		// Check if it's a record with an "id" field (Dagger object)
		if record, ok := resultValue.(map[string]interface{}); ok {
			if id, hasID := record["id"]; hasID {
				// Check if the ID is a Dagger object ID
				if idStr, ok := id.(string); ok && isDaggerObjectID(idStr) {
					// Extract just the ID for Dagger
					resultValue = idStr
					fmt.Fprintf(os.Stderr, "Extracted ID from record: %s\n", idStr)
				}
			}
		}
	}

	resultJSON, err := json.Marshal(resultValue)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal result: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "Marshaled result: %s\n", string(resultJSON))

	// Return value to Dagger
	err = fnCall.ReturnValue(ctx, dagger.JSON(string(resultJSON)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to return value: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "Success!\n")
	return 0
}
