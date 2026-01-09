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
	// Check for common Dagger object types
	daggerTypes := []string{
		"Container:", "Directory:", "File:", "Secret:",
		"Service:", "CacheVolume:", "Socket:", "Platform:",
	}
	for _, prefix := range daggerTypes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
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

	// Build Nushell command
	// We need to import the module and call the function with arguments
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
				// These should be passed as-is without extra quoting since they're
				// already strings that the Nushell functions expect
				if isDaggerObjectID(v) {
					args = append(args, fmt.Sprintf(`"%s"`, v))
				} else {
					// Regular strings get quoted
					args = append(args, fmt.Sprintf(`"%s"`, v))
				}
			case float64:
				args = append(args, fmt.Sprintf(`%v`, v))
			case bool:
				args = append(args, fmt.Sprintf(`%v`, v))
			default:
				// For complex types, pass the raw JSON
				args = append(args, fmt.Sprintf(`'%s'`, argValue))
			}
		} else {
			// If JSON parsing fails, pass as-is
			fmt.Fprintf(os.Stderr, "Warning: Failed to parse argument %s: %v\n", argName, err)
			args = append(args, fmt.Sprintf(`'%s'`, argValue))
		}
	}

	// Construct Nushell command
	nuCommand := fmt.Sprintf(`use /src/main.nu *; %s %s`, fnName, strings.Join(args, " "))

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
	// - For Dagger objects (Container, Directory, etc.): the result is already an ID string
	//   and should be marshaled as-is
	//
	// Since Nushell functions return plain values, we detect the type and marshal accordingly:
	// - If it looks like a Dagger object ID, marshal it as a string
	// - Otherwise, try to parse it as JSON first (for complex types), then fall back to string
	var resultValue interface{}

	// Try to parse as JSON first (for numbers, booleans, complex types)
	if err := json.Unmarshal([]byte(result), &resultValue); err != nil {
		// Not valid JSON, treat as string
		resultValue = result
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
