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
	argMap := make(map[string]string)

	for _, arg := range inputArgs {
		argName, _ := arg.Name(ctx)
		argValue, _ := arg.Value(ctx)
		argMap[argName] = string(argValue)

		// Build argument string for Nushell
		// Parse the JSON value and format it appropriately
		var val interface{}
		if err := json.Unmarshal([]byte(string(argValue)), &val); err == nil {
			switch v := val.(type) {
			case string:
				args = append(args, fmt.Sprintf(`"%s"`, v))
			case float64:
				args = append(args, fmt.Sprintf(`%v`, v))
			case bool:
				args = append(args, fmt.Sprintf(`%v`, v))
			default:
				args = append(args, fmt.Sprintf(`'%s'`, argValue))
			}
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
	// Dagger will handle type conversion based on the function's declared return type
	// For primitive types (string, int, bool), marshal as JSON
	// For Dagger objects (Container, Directory, etc.), the result is an ID string that gets marshaled as JSON
	resultJSON, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal result: %v\n", err)
		return 1
	}

	// Return value to Dagger
	err = fnCall.ReturnValue(ctx, dagger.JSON(string(resultJSON)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to return value: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "Success!\n")
	return 0
}
