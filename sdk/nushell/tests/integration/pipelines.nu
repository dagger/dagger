#!/usr/bin/env nu
# Integration tests - complete pipeline workflows combining multiple operations

use /usr/local/lib/dag.nu *
use /usr/local/lib/dag/wrappers.nu *

# === TEST HELPERS ===

def assert-equal [actual: any, expected: any, message: string] {
    if ($actual != $expected) {
        error make {msg: $"($message): expected ($expected), got ($actual)"}
    }
    true
}

# === CONTAINER INTEGRATION TESTS ===

# @check
export def "test-container-workflow" []: nothing -> string {
    # Test a complete container workflow
    let result = (container from "alpine"
        | with-exec ["sh", "-c", "echo 'Hello from container' && echo 'Second line'"]
        | stdout)
    
    assert-equal ($result | str contains "Hello from container") true "container workflow should execute commands"
    "test-container-workflow: PASS"
}

# @check
export def "test-container-with-multiple-env" []: nothing -> string {
    # Test setting multiple environment variables
    let result = (container from "alpine"
        | with-env-variable "FOO" "bar"
        | with-env-variable "BAZ" "qux"
        | with-exec ["sh", "-c", "echo $FOO $BAZ"]
        | stdout)
    
    assert-equal $result "bar qux" "multiple env vars should be set"
    "test-container-with-multiple-env: PASS"
}

# @check
export def "test-container-workdir" []: nothing -> string {
    # Test working directory changes
    let result = (container from "alpine"
        | with-workdir "/tmp"
        | with-exec ["pwd"]
        | stdout)
    
    assert-equal $result "/tmp" "workdir should be set correctly"
    "test-container-workdir: PASS"
}

# @check
export def "test-container-entrypoint" []: nothing -> string {
    # Test entrypoint changes
    let result = (container from "alpine"
        | with-entrypoint ["echo"]
        | with-exec ["modified-args"]
        | stdout)
    
    assert-equal $result "modified-args" "entrypoint should work with modified args"
    "test-container-entrypoint: PASS"
}

# @check
export def "test-container-with-mounted-directory" []: nothing -> string {
    # Test mounting a directory into container
    let host_dir = (host directory "/tmp")
    let result = (container from "alpine"
        | with-directory "/mnt" $host_dir
        | with-exec ["ls", "/mnt"]
        | stdout)
    
    assert-truthy ($result | str contains ".") "mounted directory should be accessible"
    "test-container-with-mounted-directory: PASS"
}

# === DIRECTORY INTEGRATION TESTS ===

# @check
export def "test-directory-create-and-modify" []: nothing -> string {
    # Test creating directory and adding files
    let dir = (host directory "/tmp")
    let dir2 = ($dir 
        | with-new-file "file1.txt" "content1"
        | with-new-file "file2.txt" "content2"
        | with-new-directory "subdir"
    )
    
    let entries = ($dir2 | entries)
    
    assert-truthy ($entries | where $it == "file1.txt" | is-not-empty) "file1.txt should exist"
    assert-truthy ($entries | where $it == "file2.txt" | is-not-empty) "file2.txt should exist"
    assert-truthy ($entries | where $it == "subdir" | is-not-empty) "subdir should exist"
    "test-directory-create-and-modify: PASS"
}

# @check
export def "test-directory-nested-operations" []: nothing -> string {
    # Test nested directory operations
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-directory "level1")
    let dir3 = ($dir2 | directory "level1" | with-new-directory "level2")
    let level2 = ($dir3 | directory "level2")
    
    assert-truthy ($level2 | get -i __type | default "" | str contains "Directory") "should return Directory"
    "test-directory-nested-operations: PASS"
}

# @check
export def "test-directory-file-operations" []: nothing -> string {
    # Test getting files and reading contents
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "Hello, World!")
    let file = ($dir2 | file "test.txt")
    let contents = ($file | contents)
    
    assert-equal $contents "Hello, World!" "file contents should match"
    "test-directory-file-operations: PASS"
}

# @check
export def "test-directory-remove-operations" []: nothing -> string {
    # Test removing files and directories
    let dir = (host directory "/tmp")
    let dir2 = ($dir 
        | with-new-file "to-remove.txt" "will be removed"
        | with-new-directory "to-remove-dir"
    )
    let dir3 = ($dir2 
        | without-file "to-remove.txt"
        | without-directory "to-remove-dir"
    )
    
    let entries = ($dir3 | entries)
    
    assert-truthy ($entries | where $it == "to-remove.txt" | is-empty) "file should be removed"
    assert-truthy ($entries | where $it == "to-remove-dir" | is-empty) "directory should be removed"
    "test-directory-remove-operations: PASS"
}

# === FILE OPERATIONS INTEGRATION ===

# @check
export def "test-file-size-and-name" []: nothing -> string {
    # Test file metadata operations
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "metadata-test.txt" "12345")
    let file = ($dir2 | file "metadata-test.txt")
    
    assert-equal ($file | size) 5 "file size should be 5"
    assert-equal ($file | name) "metadata-test.txt" "file name should be correct"
    "test-file-size-and-name: PASS"
}

# === CROSS-TYPE OPERATIONS INTEGRATION ===

# @check
export def "test-container-then-directory" []: nothing -> string {
    # Test container operations followed by directory operations
    let host_dir = (host directory "/tmp")
    let container = (container from "alpine"
        | with-directory "/src" $host_dir
        | with-workdir "/src"
    )
    
    # Now get directory from container
    let dir = ($container | directory "/src")
    let type = ($dir | get -i __type | default "unknown")
    
    assert-equal $type "Directory" "should get Directory from container"
    "test-container-then-directory: PASS"
}

# @check
export def "test-directory-then-container" []: nothing -> string {
    # Test directory operations followed by mounting in container
    let host_dir = (host directory "/tmp")
    let dir = ($host_dir | with-new-file "app.sh" "#!/bin/sh\necho 'Hello'")
    
    let result = (container from "alpine"
        | with-file "/app.sh" $dir
        | with-exec ["chmod", "+x", "/app.sh"]
        | with-exec ["/app.sh"]
        | stdout)
    
    assert-equal $result "Hello" "script from directory should execute"
    "test-directory-then-container: PASS"
}

# === CACHE AND SECRET INTEGRATION ===

# @check
export def "test-cache-volume" []: nothing -> string {
    # Test cache volume operations
    let cache = (cache-volume "test-cache-key")
    let type = ($cache | get -i __type | default "unknown")
    
    assert-equal $type "CacheVolume" "cache should have correct type"
    "test-cache-volume: PASS"
}

# @check
export def "test-secret-creation" []: nothing -> string {
    # Test secret creation
    let secret = (secret from-plaintext "my-secret-value")
    let type = ($secret | get -i __type | default "unknown")
    
    assert-equal $type "Secret" "secret should have correct type"
    "test-secret-creation: PASS"
}

# === RUN ALL INTEGRATION TESTS ===

# @check
export def "test-integration-all" []: nothing -> string {
    let results = [
        # Container integration
        (test-container-workflow)
        (test-container-with-multiple-env)
        (test-container-workdir)
        (test-container-entrypoint)
        (test-container-with-mounted-directory)
        # Directory integration
        (test-directory-create-and-modify)
        (test-directory-nested-operations)
        (test-directory-file-operations)
        (test-directory-remove-operations)
        # File integration
        (test-file-size-and-name)
        # Cross-type
        (test-container-then-directory)
        (test-directory-then-container)
        # Cache and Secret
        (test-cache-volume)
        (test-secret-creation)
    ]
    
    $"Integration tests: ($results | length) tests passed"
}
