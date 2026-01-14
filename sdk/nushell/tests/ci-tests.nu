#!/usr/bin/env nu
# Comprehensive CI tests that validate Nushell SDK runtime
# Uses direct API calls to test core functionality

use /usr/local/lib/dag/core.nu *
use /usr/local/lib/dag/container.nu *
use /usr/local/lib/dag/directory.nu *
use /usr/local/lib/dag/file.nu *
use /usr/local/lib/dag/host.nu *
use /usr/local/lib/dag/cache.nu *
use /usr/local/lib/dag/secret.nu *

print "Running Nushell SDK CI tests..."
print ""

mut test_count = 0

# === CONTAINER TESTS ===
print "=== Container Tests ==="

# Test 1: Container creation and type metadata
$test_count = $test_count + 1
let c = (container from "alpine:latest")
let c_type = ($c | get -o __type | default "missing")
if $c_type != "Container" {
    error make {msg: $"Container should have __type='Container', got '($c_type)'"}
}
print $"Test ($test_count): ✓ Container creation and __type metadata"

# Test 2: Container with-exec
$test_count = $test_count + 1
let c2 = ($c | container with-exec ["echo" "test"])
if ($c2 | get -o __type) != "Container" {
    error make {msg: "with-exec should preserve Container type"}
}
print $"Test ($test_count): ✓ Container with-exec"

# Test 3: Container with-env-variable
$test_count = $test_count + 1
let c3 = ($c | container with-env-variable "TEST_VAR" "test_value")
if ($c3 | get -o __type) != "Container" {
    error make {msg: "with-env-variable should preserve Container type"}
}
print $"Test ($test_count): ✓ Container with-env-variable"

# Test 4: Container with-workdir
$test_count = $test_count + 1
let c4 = ($c | container with-workdir "/app")
if ($c4 | get -o __type) != "Container" {
    error make {msg: "with-workdir should preserve Container type"}
}
print $"Test ($test_count): ✓ Container with-workdir"

# Test 5: Container with-entrypoint
$test_count = $test_count + 1
let c5 = ($c | container with-entrypoint ["sh"])
if ($c5 | get -o __type) != "Container" {
    error make {msg: "with-entrypoint should preserve Container type"}
}
print $"Test ($test_count): ✓ Container with-entrypoint"

# Test 6: Container with-user
$test_count = $test_count + 1
let c6 = ($c | container with-user "nobody")
if ($c6 | get -o __type) != "Container" {
    error make {msg: "with-user should preserve Container type"}
}
print $"Test ($test_count): ✓ Container with-user"

# Test 7: Container with-label
$test_count = $test_count + 1
let c7 = ($c | container with-label "version" "1.0")
if ($c7 | get -o __type) != "Container" {
    error make {msg: "with-label should preserve Container type"}
}
print $"Test ($test_count): ✓ Container with-label"

# Test 8: Container pipeline
$test_count = $test_count + 1
let c8 = ($c 
    | container with-workdir "/app"
    | container with-env-variable "ENV" "prod"
    | container with-exec ["echo" "pipeline"])
if ($c8 | get -o __type) != "Container" {
    error make {msg: "Container pipeline should preserve type"}
}
print $"Test ($test_count): ✓ Container operation pipeline"

# === DIRECTORY TESTS ===
print ""
print "=== Directory Tests ==="

# Test 9: Directory creation
$test_count = $test_count + 1
let d = (host directory "/tmp")
if ($d | get -o __type) != "Directory" {
    error make {msg: "Directory should have __type='Directory'"}
}
print $"Test ($test_count): ✓ Directory creation from host"

# Test 10: Directory with-new-file
$test_count = $test_count + 1
let d2 = ($d | directory with-new-file "test.txt" "test content")
if ($d2 | get -o __type) != "Directory" {
    error make {msg: "with-new-file should preserve Directory type"}
}
print $"Test ($test_count): ✓ Directory with-new-file"

# Test 11: Directory with-new-directory
$test_count = $test_count + 1
let d3 = ($d | directory with-new-directory "subdir")
if ($d3 | get -o __type) != "Directory" {
    error make {msg: "with-new-directory should preserve Directory type"}
}
print $"Test ($test_count): ✓ Directory with-new-directory"

# Test 12: Directory pipeline
$test_count = $test_count + 1
let d4 = ($d
    | directory with-new-directory "app"
    | directory with-new-file "app/config.json" "{}")
if ($d4 | get -o __type) != "Directory" {
    error make {msg: "Directory pipeline should preserve type"}
}
print $"Test ($test_count): ✓ Directory operation pipeline"

# === FILE TESTS ===
print ""
print "=== File Tests ==="

# Test 13: File from directory
$test_count = $test_count + 1
let d5 = ($d | directory with-new-file "myfile.txt" "file content")
let f = ($d5 | directory file "myfile.txt")
if ($f | get -o __type) != "File" {
    error make {msg: "File should have __type='File'"}
}
print $"Test ($test_count): ✓ File access from directory"

# Test 14: File name
$test_count = $test_count + 1
let fname = ($f | file name)
if $fname != "myfile.txt" {
    error make {msg: $"File name should be 'myfile.txt', got '($fname)'"}
}
print $"Test ($test_count): ✓ File name query"

# Test 15: File size
$test_count = $test_count + 1
let fsize = ($f | file size)
if $fsize == 0 {
    error make {msg: "File size should be non-zero"}
}
print $"Test ($test_count): ✓ File size query"

# === CACHE TESTS ===
print ""
print "=== Cache Tests ==="

# Test 16: Cache volume creation
$test_count = $test_count + 1
let cache = (cache-volume "test-cache")
if ($cache | get -o __type) != "CacheVolume" {
    error make {msg: "CacheVolume should have __type='CacheVolume'"}
}
print $"Test ($test_count): ✓ Cache volume creation"

# Test 17: Container with cache mount
$test_count = $test_count + 1
let c_cache = ($c | container with-mounted-cache "/cache" $cache)
if ($c_cache | get -o __type) != "Container" {
    error make {msg: "with-mounted-cache should preserve Container type"}
}
print $"Test ($test_count): ✓ Container with-mounted-cache"

# === SECRET TESTS ===
print ""
print "=== Secret Tests ==="

# Test 18: Secret creation
$test_count = $test_count + 1
let secret = (secret from-plaintext "my-secret-value")
if ($secret | get -o __type) != "Secret" {
    error make {msg: "Secret should have __type='Secret'"}
}
print $"Test ($test_count): ✓ Secret creation"

# Test 19: Container with secret as env
$test_count = $test_count + 1
let c_secret = ($c | container with-secret-variable "MY_SECRET" $secret)
if ($c_secret | get -o __type) != "Container" {
    error make {msg: "with-secret-variable should preserve Container type"}
}
print $"Test ($test_count): ✓ Container with-secret-variable"

# === CROSS-TYPE TESTS ===
print ""
print "=== Cross-Type Integration Tests ==="

# Test 20: Container with directory mount
$test_count = $test_count + 1
let c_dir = ($c | container with-directory "/mnt" $d)
if ($c_dir | get -o __type) != "Container" {
    error make {msg: "with-directory should preserve Container type"}
}
print $"Test ($test_count): ✓ Container with-directory"

# Test 21: Container with file mount
$test_count = $test_count + 1
let c_file = ($c | container with-file "/etc/config" $f)
if ($c_file | get -o __type) != "Container" {
    error make {msg: "with-file should preserve Container type"}
}
print $"Test ($test_count): ✓ Container with-file"

# Test 22: Container directory access
$test_count = $test_count + 1
let rootfs = ($c | container directory "/")
if ($rootfs | get -o __type) != "Directory" {
    error make {msg: "container directory should return Directory type"}
}
print $"Test ($test_count): ✓ Container directory access"

# Test 23: Container file access
$test_count = $test_count + 1
let d_with_file = ($d | directory with-new-file "test.txt" "content")
let c_with_dir = ($c | container with-directory "/data" $d_with_file)
let accessed_file = ($c_with_dir | container file "/data/test.txt")
if ($accessed_file | get -o __type) != "File" {
    error make {msg: "container file should return File type"}
}
print $"Test ($test_count): ✓ Container file access"

# Test 24: Complex pipeline
$test_count = $test_count + 1
let complex = (
    container from "alpine:latest"
    | container with-workdir "/app"
    | container with-directory "/app" $d
    | container with-env-variable "CACHE_DIR" "/cache"
    | container with-mounted-cache "/cache" $cache
    | container with-exec ["ls" "-la"]
)
if ($complex | get -o __type) != "Container" {
    error make {msg: "Complex pipeline should preserve Container type"}
}
print $"Test ($test_count): ✓ Complex multi-type pipeline"

print ""
print $"✅ All ($test_count) CI tests passed!"
print ""
print "Test coverage:"
print "  - Container operations: 8 tests"
print "  - Directory operations: 4 tests"
print "  - File operations: 3 tests"
print "  - Cache operations: 2 tests"
print "  - Secret operations: 2 tests"
print "  - Cross-type integration: 5 tests"
print ""
print "These tests validate the Nushell SDK runtime with live Dagger API calls,"
print "ensuring all core operations work correctly with proper type preservation."
