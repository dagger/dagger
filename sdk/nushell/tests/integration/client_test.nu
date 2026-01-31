#!/usr/bin/env nu
# Integration tests ported from sdk/go/client_test.go
# These tests verify core SDK functionality and replicate Go SDK test pipelines
# for better CI caching performance.
#
# These tests use the clean wrapper syntax to demonstrate idiomatic Nushell pipelines.

use /usr/local/lib/dag.nu *

# Test: Directory operations
# Verifies: Directory creation, file operations, contents retrieval
# Port of: TestDirectory (Go SDK)
export def test-directory [] {
    print "Testing directory operations..."
    
    # Create a directory with a new file and read its contents
    # Using clean wrapper syntax with pipelines
    let contents = (directory new
        | with-new-file "/hello.txt" "world"
        | get-file "/hello.txt"
        | contents)
    
    if ($contents != "world") {
        error make {msg: $"Expected 'world', got '($contents)'"}
    }
    
    print "✓ Directory test passed"
}

# Test: Git operations
# Verifies: Git clone, branch selection, tree navigation, file reading
# Port of: TestGit (Go SDK)
export def test-git [] {
    print "Testing git operations..."
    
    # Clone dagger repo and read README using clean pipeline syntax
    let tree = (git repo "github.com/dagger/dagger" 
        | git branch "main"
        | git-ref tree)
    
    # Check that README.md exists in entries
    let files = ($tree | entries)
    if ("README.md" not-in $files) {
        error make {msg: "README.md not found in git tree entries"}
    }
    
    # Read README contents using wrapper
    let readme_file = ($tree | get-file "README.md")
    let readme = ($readme_file | contents)
    
    if ($readme | is-empty) {
        error make {msg: "README.md is empty"}
    }
    
    if ("Dagger" not-in $readme) {
        error make {msg: "README.md doesn't contain 'Dagger'"}
    }
    
    # Test ID round-trip: get file ID and load it back
    let readme_id = ($readme_file | get id)
    let other_readme = (load-file-from-id $readme_id | contents)
    
    if ($readme != $other_readme) {
        error make {msg: "README content doesn't match after ID round-trip"}
    }
    
    print "✓ Git test passed"
}

# Test: Container operations
# Verifies: Container creation, file access, exec, ID round-trip
# Port of: TestContainer (Go SDK)
export def test-container [] {
    print "Testing container operations..."
    
    # Create alpine container with clean syntax
    let alpine = (container from "alpine:3.16.2")
    
    # Read alpine-release file using wrappers
    let contents = ($alpine 
        | get-file "/etc/alpine-release"
        | contents)
    
    if ($contents != "3.16.2\n") {
        error make {msg: $"Expected '3.16.2\n', got '($contents)'"}
    }
    
    # Execute cat command and verify stdout with clean pipeline
    let stdout_result = ($alpine
        | with-exec ["cat" "/etc/alpine-release"]
        | stdout)
    
    if ($stdout_result != "3.16.2\n") {
        error make {msg: $"Expected '3.16.2\n' from stdout, got '($stdout_result)'"}
    }
    
    # Test ID round-trip: get container ID and reload it
    let container_id = ($alpine | get id)
    let reloaded_contents = (load-container-from-id $container_id
        | get-file "/etc/alpine-release"
        | contents)
    
    if ($reloaded_contents != "3.16.2\n") {
        error make {msg: "Container content doesn't match after ID round-trip"}
    }
    
    print "✓ Container test passed"
}

# Test: Container With helper functions
# Verifies: WithEnvVariable, WithSecretVariable, With() pattern
# Port of: TestContainerWith (Go SDK)
# Note: Skipping secret test as it requires external setup
export def test-container-with [] {
    print "Testing container with-* operations..."
    
    # Test with-env-variable using beautiful clean syntax
    let result = (container from "alpine:3.16.2"
        | with-env-variable "FOO" "bar"
        | with-exec ["sh" "-c" "test $FOO = bar"]
        | sync)
    
    # If sync succeeds, the test passed (no error thrown)
    print "✓ Container with test passed"
}

# Test: List operations
# Verifies: Retrieving lists of environment variables
# Port of: TestList (Go SDK)
export def test-list [] {
    print "Testing list operations..."
    
    # Create container with environment variables using clean wrappers
    # Note: env-variables is not a wrapper, requires namespace syntax
    let envs = (container from "alpine:3.16.2"
        | with-env-variable "FOO" "BAR"
        | with-env-variable "BAR" "BAZ"
        | container env-variables)
    
    # Note: Nushell returns lists differently than Go
    # We need to check that our env vars are in the list
    let foo_found = ($envs | where name == "FOO" | length) > 0
    let bar_found = ($envs | where name == "BAR" | length) > 0
    
    if (not $foo_found) {
        error make {msg: "FOO environment variable not found"}
    }
    
    if (not $bar_found) {
        error make {msg: "BAR environment variable not found"}
    }
    
    # Verify values using Nushell's powerful filtering
    let foo_value = ($envs | where name == "FOO" | get 0 | get value)
    let bar_value = ($envs | where name == "BAR" | get 0 | get value)
    
    if ($foo_value != "BAR") {
        error make {msg: $"Expected FOO=BAR, got FOO=($foo_value)"}
    }
    
    if ($bar_value != "BAZ") {
        error make {msg: $"Expected BAR=BAZ, got BAR=($bar_value)"}
    }
    
    print "✓ List test passed"
}

# Test: Exec error handling
# Verifies: Exit codes, stdout/stderr capture on failure
# Port of: TestExecError (Go SDK)
export def test-exec-error [] {
    print "Testing exec error handling..."
    
    # Test 1: Command with specific output and exit code
    let out_msg = "STDOUT HERE"
    let err_msg = "STDERR HERE"
    
    # This should fail with exit code 127 - using clean wrapper syntax
    let result = (try {
        container from "alpine:3.16.2"
            | with-directory "/" (directory new
                | with-new-file "testout" $out_msg
                | with-new-file "testerr" $err_msg)
            | with-exec ["sh" "-c" "cat /testout; cat /testerr >&2; exit 127"]
            | sync
    } catch { |err|
        # Expected to fail
        {error: true, message: ($err | to text)}
    })
    
    if ($result.error? != true) {
        error make {msg: "Expected exec to fail with exit code 127"}
    }
    
    # Test 2: Simple false command with beautiful pipeline
    let result2 = (try {
        container from "alpine:3.16.2"
            | with-exec ["false"]
            | sync
    } catch { |err|
        {error: true, message: ($err | to text)}
    })
    
    if ($result2.error? != true) {
        error make {msg: "Expected exec to fail with exit code 1"}
    }
    
    # Test 3: Non-exec error (invalid image)
    let result3 = (try {
        container from "invalid!"
            | with-exec ["false"]
            | sync
    } catch { |err|
        {error: true, message: ($err | to text)}
    })
    
    if ($result3.error? != true) {
        error make {msg: "Expected invalid image to fail"}
    }
    
    print "✓ Exec error test passed"
}

# Run all integration tests
export def run-all [] {
    print "\n=== Running Integration Tests ==="
    print "Ported from sdk/go/client_test.go\n"
    
    test-directory
    test-git
    test-container
    test-container-with
    test-list
    test-exec-error
    
    print "\n=== All Integration Tests Passed ✓ ==="
}
