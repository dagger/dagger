#!/usr/bin/env nu
# Nushell SDK Test Runner
# Run with: nu tests/run.nu

# Source the SDK
use runtime/dag.nu *
use runtime/dag/wrappers.nu *

# === TEST HELPERS ===

def assert-equal [actual: any, expected: any, message: string] {
    if ($actual != $expected) {
        print $"FAILED: ($message)"
        print $"  Expected: ($expected)"
        print $"  Got: ($actual)"
        return false
    }
    true
}

def assert-truthy [value: any, message: string] {
    if ($value | describe) == "bool" and $value == false {
        print $"FAILED: ($message) - got false"
        return false
    }
    if ($value == null) {
        print $"FAILED: ($message) - got null"
        return false
    }
    true
}

def print-pass [name: string] {
    print $"✓ ($name)"
}

# === CORE TESTS (run locally without dagger) ===

export def "run-core-tests" [] {
    print "\n=== Core Tests ==="
    let results = []
    
    # Test type metadata on container
    let container = (container from "alpine")
    if (assert-equal ($container | get -o __type) "Container" "container __type") {
        $results = ($results | append "test-type-metadata-container")
    }
    
    # Test type metadata on directory  
    let dir = (host directory "/tmp")
    if (assert-equal ($dir | get -o __type) "Directory" "directory __type") {
        $results = ($results | append "test-type-metadata-directory")
    }
    
    # Test get-object-type
    let type = (get-object-type $container)
    if (assert-equal $type "Container" "get-object-type") {
        $results = ($results | append "test-get-object-type")
    }
    
    print $"Core tests: ($results | length) passed"
}

# === RUN TESTS ===

run-core-tests
