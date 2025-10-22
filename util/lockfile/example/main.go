package main

import (
	"fmt"
	"log"

	"github.com/dagger/dagger/util/lockfile"
)

func main() {
	fmt.Println("=== Lockfile Non-Deterministic Type Validation Example ===\n")

	// Create a new lockfile
	lf := lockfile.New()

	// Example 1: Valid usage with deterministic types
	fmt.Println("1. Valid usage with deterministic types:")
	args1 := []lockfile.FunctionArg{
		{Name: "image", Value: "alpine:latest"},
		{Name: "platform", Value: "linux/amd64"},
	}
	err := lf.Set("container", "from", args1, "sha256:abc123")
	if err != nil {
		fmt.Printf("   ❌ Error: %v\n", err)
	} else {
		fmt.Println("   ✅ Success: Set entry with string arguments and result")
	}

	// Example 2: Valid usage with arrays
	fmt.Println("\n2. Valid usage with arrays:")
	args2 := []lockfile.FunctionArg{
		{Name: "files", Value: []string{"main.go", "util.go", "test.go"}},
		{Name: "flags", Value: []string{"-o", "binary", "-v"}},
	}
	err = lf.Set("build", "compile", args2, []string{"binary", "binary.sha256"})
	if err != nil {
		fmt.Printf("   ❌ Error: %v\n", err)
	} else {
		fmt.Println("   ✅ Success: Set entry with array arguments and result")
	}

	// Example 3: Invalid usage - map in arguments
	fmt.Println("\n3. Invalid usage - map in arguments:")
	args3 := []lockfile.FunctionArg{
		{Name: "config", Value: map[string]interface{}{
			"host": "localhost",
			"port": 8080,
		}},
	}
	err = lf.Set("server", "configure", args3, "configured")
	if err != nil {
		fmt.Printf("   ❌ Expected error: %v\n", err)
	} else {
		fmt.Println("   ✅ Success (unexpected!)")
	}

	// Example 4: Invalid usage - map in result
	fmt.Println("\n4. Invalid usage - map in result:")
	args4 := []lockfile.FunctionArg{
		{Name: "service", Value: "api"},
	}
	result4 := map[string]interface{}{
		"status": "healthy",
		"uptime": 3600,
	}
	err = lf.Set("health", "check", args4, result4)
	if err != nil {
		fmt.Printf("   ❌ Expected error: %v\n", err)
	} else {
		fmt.Println("   ✅ Success (unexpected!)")
	}

	// Example 5: Invalid usage - nested map in array
	fmt.Println("\n5. Invalid usage - nested map in array:")
	args5 := []lockfile.FunctionArg{
		{Name: "items", Value: []interface{}{
			"string-item",
			123,
			map[string]string{"nested": "map"}, // This makes it non-deterministic
		}},
	}
	err = lf.Set("batch", "process", args5, "result")
	if err != nil {
		fmt.Printf("   ❌ Expected error: %v\n", err)
	} else {
		fmt.Println("   ✅ Success (unexpected!)")
	}

	// Example 6: Valid usage with struct (no maps)
	fmt.Println("\n6. Valid usage with struct (no maps):")
	type Config struct {
		Name    string
		Version string
		Ports   []int
		Debug   bool
	}
	args6 := []lockfile.FunctionArg{
		{Name: "config", Value: Config{
			Name:    "myapp",
			Version: "1.0.0",
			Ports:   []int{8080, 8443},
			Debug:   true,
		}},
	}
	err = lf.Set("deploy", "service", args6, "deployment-id-123")
	if err != nil {
		fmt.Printf("   ❌ Error: %v\n", err)
	} else {
		fmt.Println("   ✅ Success: Set entry with struct argument (no maps)")
	}

	// Example 7: Get with non-deterministic arguments returns nil
	fmt.Println("\n7. Get with non-deterministic arguments:")
	args7 := []lockfile.FunctionArg{
		{Name: "settings", Value: map[string]string{
			"key": "value",
		}},
	}
	result := lf.Get("test", "function", args7)
	if result == nil {
		fmt.Println("   ✅ Expected: Get returns nil for non-deterministic arguments")
	} else {
		fmt.Printf("   ❌ Unexpected: Got result %v\n", result)
	}

	// Example 8: Retrieve previously set deterministic entry
	fmt.Println("\n8. Retrieve previously set entry:")
	result = lf.Get("container", "from", args1)
	if result != nil {
		fmt.Printf("   ✅ Success: Retrieved result: %v\n", result)
	} else {
		fmt.Println("   ❌ Error: Could not retrieve entry")
	}

	fmt.Println("\n=== Summary ===")
	fmt.Println("The lockfile package now validates that all arguments and return values")
	fmt.Println("can be JSON-encoded deterministically. Maps (objects) are rejected because")
	fmt.Println("their key ordering is non-deterministic in Go's JSON encoding.")
	fmt.Println("\nUse arrays, slices, structs (without map fields), and primitive types instead.")

	// Save to demonstrate it works with valid entries
	if err := lf.Save("example.lock"); err != nil {
		log.Printf("Failed to save lockfile: %v", err)
	} else {
		fmt.Println("\n✅ Lockfile saved successfully to example.lock")
	}
}
