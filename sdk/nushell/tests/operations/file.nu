#!/usr/bin/env nu
# File operation tests

use /usr/local/lib/dag.nu *

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

# === FILE CONTENT OPERATIONS ===

# @check
export def "test-file-contents" []: nothing -> string {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "Hello, File!")
    let file = ($dir2 | file "test.txt")
    let contents = ($file | contents)
    assert-equal $contents "Hello, File!" "file contents should match"
    "test-file-contents: PASS"
}

# @check
export def "test-file-size" []: nothing -> string {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "size-test.txt" "12345")
    let file = ($dir2 | file "size-test.txt")
    let size = ($file | size)
    assert-equal $size 5 "file size should be 5"
    "test-file-size: PASS"
}

# @check
export def "test-file-name" []: nothing -> string {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "my-file.txt" "content")
    let file = ($dir2 | file "my-file.txt")
    let name = ($file | name)
    assert-equal $name "my-file.txt" "file name should match"
    "test-file-name: PASS"
}

# @check
export def "test-file-export" []: nothing -> string {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "export-test.txt" "export me")
    let file = ($dir2 | file "export-test.txt")
    let result = ($file | export "/tmp/file-export-result.txt")
    assert-equal $result true "file export should return true"
    "test-file-export: PASS"
}

# === RUN ALL FILE TESTS ===

# @check
export def "test-file-all" []: nothing -> string {
    let results = [
        (test-file-contents)
        (test-file-size)
        (test-file-name)
        (test-file-export)
    ]
    
    $"File tests: ($results | length) tests passed"
}
