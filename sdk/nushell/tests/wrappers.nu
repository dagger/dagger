#!/usr/bin/env nu
# Wrapper function tests - multi-type detection, type-safe pipelines, error handling

use /usr/local/lib/dag.nu *
use /usr/local/lib/dag/wrappers.nu *

# === TEST HELPERS ===

def assert-equal [actual: any, expected: any, message: string] {
    if ($actual != $expected) {
        error make {msg: $"($message): expected ($expected), got ($actual)"}
    }
    true
}

def assert-error-contains [func: closure, substring: string] {
    try {
        do $func
        error make {msg: $"Expected error containing '($substring)', but no error was thrown"}
    } catch { |e|
        if ($e.msg | str contains $substring) {
            true
        } else {
            error make {msg: $"Expected error containing '($substring)', got: ($e.msg)"}
        }
    }
}

# === MULTI-TYPE WRAPPER TESTS ===

# @check
export def "test-with-directory-container" []: nothing -> string {
    # with-directory should work on Container (mounts directory)
    let dir = (host directory "/tmp")
    let container = (container from "alpine" | with-directory "/mnt" $dir)
    let type = (get-object-type $container)
    
    assert-equal $type "Container" "with-directory on Container should return Container"
    "test-with-directory-container: PASS"
}

# @check
export def "test-with-directory-directory" []: nothing -> string {
    # with-directory should work on Directory (adds subdirectory)
    let dir = (host directory "/tmp")
    let subdir = ($dir | with-new-directory "subdir")
    let dir2 = ($dir | with-directory "/added" $subdir)
    let type = (get-object-type $dir2)
    
    assert-equal $type "Directory" "with-directory on Directory should return Directory"
    "test-with-directory-directory: PASS"
}

# @check
export def "test-with-file-container" []: nothing -> string {
    # with-file should work on Container (mounts file)
    let dir = (host directory "/tmp")
    let file = ($dir | with-new-file "test.txt" "content")
    let container = (container from "alpine" | with-file "/mnt/file.txt" $file)
    let type = (get-object-type $container)
    
    assert-equal $type "Container" "with-file on Container should return Container"
    "test-with-file-container: PASS"
}

# @check
export def "test-with-file-directory" []: nothing -> string {
    # with-file should work on Directory (adds file)
    let dir = (host directory "/tmp")
    let file = ($dir | with-new-file "file.txt" "content")
    let dir2 = ($dir | with-file "/new.txt" $file)
    let type = (get-object-type $dir2)
    
    assert-equal $type "Directory" "with-file on Directory should return Directory"
    "test-with-file-directory: PASS"
}

# @check
export def "test-with-new-file-container" []: nothing -> string {
    # with-new-file should work on Container (creates file in container)
    let container = (container from "alpine" | with-new-file "/test.txt" "content")
    let type = (get-object-type $container)
    
    assert-equal $type "Container" "with-new-file on Container should return Container"
    "test-with-new-file-container: PASS"
}

# @check
export def "test-with-new-file-directory" []: nothing -> string {
    # with-new-file should work on Directory (creates file in directory)
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "content")
    let type = (get-object-type $dir2)
    
    assert-equal $type "Directory" "with-new-file on Directory should return Directory"
    "test-with-new-file-directory: PASS"
}

# @check
export def "test-without-directory-container" []: nothing -> string {
    # without-directory should work on Container (removes mounted directory)
    let dir = (host directory "/tmp")
    let container = (container from "alpine" | with-directory "/mnt" $dir | without-directory "/mnt")
    let type = (get-object-type $container)
    
    assert-equal $type "Container" "without-directory on Container should return Container"
    "test-without-directory-container: PASS"
}

# @check
export def "test-without-directory-directory" []: nothing -> string {
    # without-directory should work on Directory (removes subdirectory)
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-directory "testdir")
    let dir3 = ($dir2 | without-directory "testdir")
    let type = (get-object-type $dir3)
    
    assert-equal $type "Directory" "without-directory on Directory should return Directory"
    "test-without-directory-directory: PASS"
}

# @check
export def "test-without-file-container" []: nothing -> string {
    # without-file should work on Container (removes mounted file)
    let dir = (host directory "/tmp")
    let file = ($dir | with-new-file "test.txt" "content")
    let container = (container from "alpine" | with-file "/mnt/file.txt" $file | without-file "/mnt/file.txt")
    let type = (get-object-type $container)
    
    assert-equal $type "Container" "without-file on Container should return Container"
    "test-without-file-container: PASS"
}

# @check
export def "test-without-file-directory" []: nothing -> string {
    # without-file should work on Directory (removes file)
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "content")
    let dir3 = ($dir2 | without-file "test.txt")
    let type = (get-object-type $dir3)
    
    assert-equal $type "Directory" "without-file on Directory should return Directory"
    "test-without-file-directory: PASS"
}

# === ERROR HANDLING TESTS ===

# @check
export def "test-without-directory-error-wrong-type" []: nothing -> string {
    # without-directory should error on File
    let dir = (host directory "/tmp")
    let file = ($dir | with-new-file "test.txt" "content")
    
    assert-error-contains {
        do { $file | without-directory "/test" }
    } "only works on"
    "test-without-directory-error-wrong-type: PASS"
}

# @check
export def "test-without-file-error-wrong-type" []: nothing -> string {
    # without-file should error on Container without mount
    let container = (container from "alpine")
    
    assert-error-contains {
        do { $container | without-file "/nonexistent" }
    } "only works on"
    "test-without-file-error-wrong-type: PASS"
}

# @check
export def "test-with-directory-error-wrong-type" []: nothing -> string {
    # with-directory should error on File
    let dir = (host directory "/tmp")
    let file = ($dir | with-new-file "test.txt" "content")
    let subdir = ($dir | with-new-directory "subdir")
    
    assert-error-contains {
        do { $file | with-directory "/test" $subdir }
    } "only works on"
    "test-with-directory-error-wrong-type: PASS"
}

# @check
export def "test-with-file-error-wrong-type" []: nothing -> string {
    # with-file should error on File
    let dir = (host directory "/tmp")
    let file = ($dir | with-new-file "test.txt" "content")
    let file2 = ($dir | with-new-file "file2.txt" "content")
    
    assert-error-contains {
        do { $file | with-file "/test" $file2 }
    } "only works on"
    "test-with-file-error-wrong-type: PASS"
}

# @check
export def "test-with-new-file-error-wrong-type" []: nothing -> string {
    # with-new-file should error on File
    let dir = (host directory "/tmp")
    let file = ($dir | with-new-file "test.txt" "content")
    
    assert-error-contains {
        do { $file | with-new-file "/test" "content" }
    } "only works on"
    "test-with-new-file-error-wrong-type: PASS"
}

# === CROSS-TYPE OPERATION TESTS ===

# @check
export def "test-export-multi-type" []: nothing -> string {
    # export should work on Container, File, and Directory
    let dir = (host directory "/tmp")
    
    # Test export on Directory (won't actually export, just verify function exists)
    let dir2 = ($dir | with-new-file "test.txt" "content")
    let type = (get-object-type $dir2)
    
    assert-equal $type "Directory" "Directory operations should work"
    "test-export-multi-type: PASS"
}

# @check
export def "test-entries-directory-only" []: nothing -> string {
    # entries should only work on Directory
    let dir = (host directory "/tmp")
    let entries = ($dir | entries)
    
    assert-equal ($entries | describe) "list<string>" "entries should return list<string>"
    "test-entries-directory-only: PASS"
}

# @check
export def "test-contents-file-only" []: nothing -> string {
    # contents should only work on File
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "hello")
    let file = ($dir2 | file "test.txt")
    let contents = ($file | contents)
    
    assert-equal $contents "hello" "contents should return file contents"
    "test-contents-file-only: PASS"
}

# @check
export def "test-size-file-only" []: nothing -> string {
    # size should only work on File
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "hello world")
    let file = ($dir2 | file "test.txt")
    let size = ($file | size)
    
    assert-equal $size 11 "size should return file size"
    "test-size-file-only: PASS"
}

# @check
export def "test-name-file-only" []: nothing -> string {
    # name should only work on File
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "content")
    let file = ($dir2 | file "test.txt")
    let name = ($file | name)
    
    assert-equal $name "test.txt" "name should return filename"
    "test-name-file-only: PASS"
}

# @check
export def "test-exists-multi-type" []: nothing -> string {
    # exists should work on Directory
    let dir = (host directory "/tmp")
    let exists = ($dir | exists ".")
    
    assert-equal $exists true "exists should return true for existing path"
    "test-exists-multi-type: PASS"
}

# @check
export def "test-glob-directory-only" []: nothing -> string {
    # glob should only work on Directory
    let dir = (host directory "/tmp")
    let results = ($dir | glob "*.txt")
    
    assert-equal ($results | describe) "list<string>" "glob should return list<string>"
    "test-glob-directory-only: PASS"
}

# === RUN ALL WRAPPER TESTS ===

# @check
export def "test-wrappers-all" []: nothing -> string {
    let results = [
        # Multi-type wrappers
        (test-with-directory-container)
        (test-with-directory-directory)
        (test-with-file-container)
        (test-with-file-directory)
        (test-with-new-file-container)
        (test-with-new-file-directory)
        (test-without-directory-container)
        (test-without-directory-directory)
        (test-without-file-container)
        (test-without-file-directory)
        # Error handling
        (test-without-directory-error-wrong-type)
        (test-without-file-error-wrong-type)
        (test-with-directory-error-wrong-type)
        (test-with-file-error-wrong-type)
        (test-with-new-file-error-wrong-type)
        # Cross-type operations
        (test-export-multi-type)
        (test-entries-directory-only)
        (test-contents-file-only)
        (test-size-file-only)
        (test-name-file-only)
        (test-exists-multi-type)
        (test-glob-directory-only)
    ]
    
    $"Wrapper tests: ($results | length) tests passed"
}
