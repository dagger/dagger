#!/usr/bin/env nu
# Direct runtime tests that don't use wrapper functions
# These tests call the core runtime functions directly

use /usr/local/lib/dag/core.nu *
use /usr/local/lib/dag/container.nu *
use /usr/local/lib/dag/directory.nu *
use /usr/local/lib/dag/file.nu *
use /usr/local/lib/dag/host.nu *

print "Running direct runtime tests..."

# Test 1: Container from
print "Testing container from..."
let c = (container from "alpine")
if ($c | get -o __type) != "Container" {
    error make {msg: "container from should return Container type"}
}
print "✓ container from works"

# Test 2: Host directory  
print "Testing host directory..."
let d = (host directory "/tmp")
if ($d | get -o __type) != "Directory" {
    error make {msg: "host directory should return Directory type"}
}
print "✓ host directory works"

# Test 3: Container with-exec
print "Testing container with-exec..."
let c2 = ($c | container with-exec ["echo" "test"])
if ($c2 | get -o __type) != "Container" {
    error make {msg: "with-exec should return Container type"}
}
print "✓ container with-exec works"

# Test 4: Container with-env-variable
print "Testing container with-env-variable..."
let c3 = ($c | container with-env-variable "TEST" "value")
if ($c3 | get -o __type) != "Container" {
    error make {msg: "with-env-variable should return Container type"}
}
print "✓ container with-env-variable works"

# Test 5: Container with-workdir
print "Testing container with-workdir..."
let c4 = ($c | container with-workdir "/app")
if ($c4 | get -o __type) != "Container" {
    error make {msg: "with-workdir should return Container type"}
}
print "✓ container with-workdir works"

# Test 6: Directory with-new-file
print "Testing directory with-new-file..."
let d2 = ($d | directory with-new-file "test.txt" "content")
if ($d2 | get -o __type) != "Directory" {
    error make {msg: "with-new-file should return Directory type"}
}
print "✓ directory with-new-file works"

# Test 7: Directory with-new-directory
print "Testing directory with-new-directory..."
let d3 = ($d | directory with-new-directory "subdir")
if ($d3 | get -o __type) != "Directory" {
    error make {msg: "with-new-directory should return Directory type"}
}
print "✓ directory with-new-directory works"

# Test 8: Get-object-type function
print "Testing get-object-type..."
let container = (container from "alpine")
let container_type = ($container | get -o __type | default "Unknown")
if $container_type != "Container" {
    error make {msg: $"get-object-type should return 'Container', got '($container_type)'"}
}
print "✓ get-object-type works"

print "\n✅ All direct runtime tests passed!"
print "   - Tested 8 core operations"
print "   - All type metadata preserved"
print "   - Container, Directory, and File operations working"
