#!/usr/bin/env nu
# Directory operation tests

use /usr/local/lib/dag.nu *
use /usr/local/lib/dag/wrappers.nu *

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
    true
}

# === BASIC DIRECTORY OPERATIONS ===

# @check
export def "test-directory-from" []: nothing -> string {
    let dir = (host directory "/tmp")
    assert-truthy ($dir | get -i id | is-not-null) "directory should have id"
    assert-equal ($dir | get -i __type) "Directory" "directory should have __type: Directory"
    "test-directory-from: PASS"
}

# @check
export def "test-directory-new" []: nothing -> string {
    let dir = (directory new)
    assert-truthy ($dir | get -i id | is-not-null) "new directory should have id"
    "test-directory-new: PASS"
}

# @check
export def "test-directory-entries" []: nothing -> string {
    let dir = (host directory "/tmp")
    let entries = ($dir | entries)
    assert-equal ($entries | describe) "list<string>" "entries should return list<string>"
    "test-directory-entries: PASS"
}

# === FILE OPERATIONS ===

# @check
export def "test-directory-file" []: nothing -> string {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "content")
    let file = ($dir2 | file "test.txt")
    assert-truthy ($file | get -i id | is-not-null) "file should have id"
    assert-equal ($file | get -i __type) "File" "file should have __type: File"
    "test-directory-file: PASS"
}

# @check
export def "test-directory-directory" []: nothing -> string {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-directory "subdir")
    let subdir = ($dir2 | directory "subdir")
    assert-truthy ($subdir | get -i id | is-not-null) "subdirectory should have id"
    assert-equal ($subdir | get -i __type) "Directory" "subdirectory should have __type: Directory"
    "test-directory-directory: PASS"
}

# === MODIFY OPERATIONS ===

# @check
export def "test-directory-with-file" []: nothing -> string {
    let host_dir = (host directory "/tmp")
    let file = ($host_dir | with-new-file "source.txt" "source content")
    let dir = ($host_dir | with-file "/copied.txt" $file)
    let retrieved = ($dir | file "copied.txt")
    let contents = ($retrieved | contents)
    assert-equal $contents "source content" "with-file should add file to directory"
    "test-directory-with-file: PASS"
}

# @check
export def "test-directory-with-directory" []: nothing -> string {
    let host_dir = (host directory "/tmp")
    let subdir = ($host_dir | with-new-directory "source-subdir")
    let dir = ($host_dir | with-directory "/added" $subdir)
    let added = ($dir | directory "added")
    assert-truthy ($added | get -i id | is-not-null) "with-directory should add subdirectory"
    "test-directory-with-directory: PASS"
}

# @check
export def "test-directory-with-new-file" []: nothing -> string {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "new-file.txt" "new content")
    let file = ($dir2 | file "new-file.txt")
    let contents = ($file | contents)
    assert-equal $contents "new content" "with-new-file should create file"
    "test-directory-with-new-file: PASS"
}

# @check
export def "test-directory-with-new-directory" []: nothing -> string {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-directory "new-subdir")
    let entries = ($dir2 | entries)
    assert-truthy ($entries | where $it == "new-subdir" | is-not-empty) "with-new-directory should create subdirectory"
    "test-directory-with-new-directory: PASS"
}

# === REMOVE OPERATIONS ===

# @check
export def "test-directory-without-file" []: nothing -> string {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "to-remove.txt" "content")
    let dir3 = ($dir2 | without-file "to-remove.txt")
    let entries = ($dir3 | entries)
    assert-truthy ($entries | where $it == "to-remove.txt" | is-empty) "without-file should remove file"
    "test-directory-without-file: PASS"
}

# @check
export def "test-directory-without-directory" []: nothing -> string {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-directory "to-remove")
    let dir3 = ($dir2 | without-directory "to-remove")
    let entries = ($dir3 | entries)
    assert-truthy ($entries | where $it == "to-remove" | is-empty) "without-directory should remove subdirectory"
    "test-directory-without-directory: PASS"
}

# @check
export def "test-directory-without" []: nothing -> string {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "remove-me.txt" "content")
    let dir3 = ($dir2 | without "remove-me.txt")
    let entries = ($dir3 | entries)
    assert-truthy ($entries | where $it == "remove-me.txt" | is-empty) "without should remove file"
    "test-directory-without: PASS"
}

# === EXPORT OPERATION ===

# @check
export def "test-directory-export" []: nothing -> string {
    # Note: This creates a real file on disk
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "export-test.txt" "export content")
    let result = ($dir2 | export "/tmp/export-test-result.txt")
    assert-equal $result true "export should return true on success"
    "test-directory-export: PASS"
}

# === QUERY OPERATIONS ===

# @check
export def "test-directory-exists" []: nothing -> string {
    let dir = (host directory "/tmp")
    let exists = ($dir | exists ".")
    assert-equal $exists true "exists should return true for existing path"
    let not_exists = ($dir | exists "/nonexistent")
    assert-equal $not_exists false "exists should return false for non-existing path"
    "test-directory-exists: PASS"
}

# @check
export def "test-directory-glob" []: nothing -> string {
    let dir = (host directory "/tmp")
    let results = ($dir | glob "*.txt")
    assert-equal ($results | describe) "list<string>" "glob should return list<string>"
    "test-directory-glob: PASS"
}

# === RUN ALL DIRECTORY TESTS ===

# @check
export def "test-directory-all" []: nothing -> string {
    let results = [
        (test-directory-from)
        (test-directory-new)
        (test-directory-entries)
        (test-directory-file)
        (test-directory-directory)
        (test-directory-with-file)
        (test-directory-with-directory)
        (test-directory-with-new-file)
        (test-directory-with-new-directory)
        (test-directory-without-file)
        (test-directory-without-directory)
        (test-directory-without)
        (test-directory-export)
        (test-directory-exists)
        (test-directory-glob)
    ]
    
    $"Directory tests: ($results | length) tests passed"
}
