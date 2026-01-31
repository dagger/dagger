#!/usr/bin/env nu
# Core tests for __type metadata and get-object-type function

use /usr/local/lib/dag.nu *
use /usr/local/lib/dag/wrappers.nu *

# === TEST HELPERS ===

def assert-equal [actual: any, expected: any, message: string] {
    if ($actual != $expected) {
        error make {msg: $"($message): expected ($expected), got ($actual)"}
    }
    true
}

def assert-truthy [value: any, message: string] {
    if ($value | describe) == "bool" and $value == false {
        error make {msg: $"($message): expected truthy, got false"}
    }
    if ($value == null) {
        error make {msg: $"($message): expected truthy, got null"}
    }
    true
}

# === __TYPE METADATA TESTS ===

# @check
export def "test-type-metadata-present" []: nothing -> string {
    # Test that container from returns __type
    let container = (container from "alpine")
    let has_type = ($container | get -o __type | is-not-null)
    
    assert-equal $has_type true "container should have __type metadata"
    "test-type-metadata-present: PASS"
}

# @check
export def "test-type-metadata-value" []: nothing -> string {
    # Test that __type has correct value
    let container = (container from "alpine")
    let type = ($container | get -o __type | default "missing")
    
    assert-equal $type "Container" "container __type should be 'Container'"
    "test-type-metadata-value: PASS"
}

# @check
export def "test-directory-type-metadata" []: nothing -> string {
    # Test directory __type
    let dir = (host directory "/tmp")
    let type = ($dir | get -o __type | default "missing")
    
    assert-equal $type "Directory" "directory __type should be 'Directory'"
    "test-directory-type-metadata: PASS"
}

# @check
export def "test-file-type-metadata" []: nothing -> string {
    # Test file __type (from directory with-new-file)
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "content")
    let file = ($dir2 | file "test.txt")
    let type = ($file | get -o __type | default "missing")
    
    assert-equal $type "File" "file __type should be 'File'"
    "test-file-type-metadata: PASS"
}

# @check
export def "test-git-type-metadata" []: nothing -> string {
    # Test git repository __type
    let git = (git repo "https://github.com/example/repo")
    let type = ($git | get -o __type | default "missing")
    
    assert-equal $type "GitRepository" "git __type should be 'GitRepository'"
    "test-git-type-metadata: PASS"
}

# @check
export def "test-cache-type-metadata" []: nothing -> string {
    # Test cache volume __type
    let cache = (cache-volume "test-cache")
    let type = ($cache | get -o __type | default "missing")
    
    assert-equal $type "CacheVolume" "cache __type should be 'CacheVolume'"
    "test-cache-type-metadata: PASS"
}

# @check
export def "test-secret-type-metadata" []: nothing -> string {
    # Test secret __type
    let secret = (secret from-plaintext "my-secret")
    let type = ($secret | get -o __type | default "missing")
    
    assert-equal $type "Secret" "secret __type should be 'Secret'"
    "test-secret-type-metadata: PASS"
}

# @check
export def "test-service-type-metadata" []: nothing -> string {
    # Test service __type (from container as-service)
    let svc = (container from "alpine" | as-service)
    let type = ($svc | get -o __type | default "missing")
    
    assert-equal $type "Service" "service __type should be 'Service'"
    "test-service-type-metadata: PASS"
}

# === GET-OBJECT-TYPE FUNCTION TESTS ===

# @check
export def "test-get-object-type-container" []: nothing -> string {
    let container = (container from "alpine")
    let type = (get-object-type $container)
    
    assert-equal $type "Container" "get-object-type should return 'Container'"
    "test-get-object-type-container: PASS"
}

# @check
export def "test-get-object-type-directory" []: nothing -> string {
    let dir = (host directory "/tmp")
    let type = (get-object-type $dir)
    
    assert-equal $type "Directory" "get-object-type should return 'Directory'"
    "test-get-object-type-directory: PASS"
}

# @check
export def "test-get-object-type-file" []: nothing -> string {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "content")
    let file = ($dir2 | file "test.txt")
    let type = (get-object-type $file)
    
    assert-equal $type "File" "get-object-type should return 'File'"
    "test-get-object-type-file: PASS"
}

# @check
export def "test-get-object-type-unknown" []: nothing -> string {
    # Test object without __type returns "Unknown"
    let obj = {id: "test-id"}
    let type = (get-object-type $obj)
    
    assert-equal $type "Unknown" "get-object-type should return 'Unknown' for missing __type"
    "test-get-object-type-unknown: PASS"
}

# === TYPE PRESERVATION THROUGH OPERATIONS ===

# @check
export def "test-type-preserved-through-with-exec" []: nothing -> string {
    let container = (container from "alpine" | with-exec ["echo", "test"])
    let type = (get-object-type $container)
    
    assert-equal $type "Container" "type should be preserved after with-exec"
    "test-type-preserved-through-with-exec: PASS"
}

# @check
export def "test-type-preserved-through-with-env" []: nothing -> string {
    let container = (container from "alpine" | with-env-variable "TEST" "value")
    let type = (get-object-type $container)
    
    assert-equal $type "Container" "type should be preserved after with-env-variable"
    "test-type-preserved-through-with-env: PASS"
}

# @check
export def "test-type-preserved-through-with-file" []: nothing -> string {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "content")
    let type = (get-object-type $dir2)
    
    assert-equal $type "Directory" "type should be preserved after with-new-file"
    "test-type-preserved-through-with-file: PASS"
}

# @check
export def "test-type-preserved-through-with-directory" []: nothing -> string {
    let dir = (host directory "/tmp")
    let subdir = ($dir | with-new-directory "subdir")
    let type = (get-object-type $subdir)
    
    assert-equal $type "Directory" "type should be preserved after with-new-directory"
    "test-type-preserved-through-with-directory: PASS"
}

# === RUN ALL CORE TESTS ===

# @check
export def "test-core-all" []: nothing -> string {
    let results = [
        (test-type-metadata-present)
        (test-type-metadata-value)
        (test-directory-type-metadata)
        (test-file-type-metadata)
        (test-git-type-metadata)
        (test-cache-type-metadata)
        (test-secret-type-metadata)
        (test-service-type-metadata)
        (test-get-object-type-container)
        (test-get-object-type-directory)
        (test-get-object-type-file)
        (test-get-object-type-unknown)
        (test-type-preserved-through-with-exec)
        (test-type-preserved-through-with-env)
        (test-type-preserved-through-with-file)
        (test-type-preserved-through-with-directory)
    ]
    
    $"Core tests: ($results | length) tests passed"
}
