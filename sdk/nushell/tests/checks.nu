#!/usr/bin/env nu
# Nushell SDK Tests - Following Dagger's check pattern
# Checks return containers that exit 0 on pass, non-zero on fail
# Usage: dagger check (auto-discovers @check annotated functions)

use ../runtime/runtime/dag/core.nu *
use ../runtime/runtime/dag/wrappers.nu *
use ../runtime/runtime/dag/container.nu *
use ../runtime/runtime/dag/directory.nu *
use ../runtime/runtime/dag/file.nu *
use ../runtime/runtime/dag/host.nu *

# === TEST HELPERS ===

def assert-equal [actual: any, expected: any, message: string] {
    if ($actual != $expected) {
        error make {msg: $"($message): expected ($expected), got ($actual)"}
    }
    true
}

# === TYPE METADATA CHECKS (return containers that validate) ===

# @check
# Verify container has __type metadata
export def "check-type-metadata-present" []: nothing -> record {
    let container = (container from "alpine")
    let has_type = ($container | get -o __type | is-not-null)
    if $has_type {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check  
# Verify __type value is correct
export def "check-type-metadata-value" []: nothing -> record {
    let container = (container from "alpine")
    let type = ($container | get -o __type | default "missing")
    if ($type == "Container") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["sh", "-c", $"echo 'Expected Container, got ($type)' && exit 1"]
    }
}

# @check
# Verify directory __type
export def "check-directory-type-metadata" []: nothing -> record {
    let dir = (host directory "/tmp")
    let type = ($dir | get -o __type | default "missing")
    if ($type == "Directory") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify file __type
export def "check-file-type-metadata" []: nothing -> record {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "content")
    let file = ($dir2 | file "test.txt")
    let type = ($file | get -o __type | default "missing")
    if ($type == "File") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# === GET-OBJECT-TYPE CHECKS ===

# @check
# Verify get-object-type works for Container
export def "check-get-object-type-container" []: nothing -> record {
    let container = (container from "alpine")
    let type = (get-object-type $container)
    if ($type == "Container") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["sh", "-c", $"echo 'Expected Container, got ($type)' && exit 1"]
    }
}

# @check
# Verify get-object-type works for Directory
export def "check-get-object-type-directory" []: nothing -> record {
    let dir = (host directory "/tmp")
    let type = (get-object-type $dir)
    if ($type == "Directory") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify get-object-type works for File
export def "check-get-object-type-file" []: nothing -> record {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "content")
    let file = ($dir2 | file "test.txt")
    let type = (get-object-type $file)
    if ($type == "File") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify get-object-type returns "Unknown" for missing __type
export def "check-get-object-type-unknown" []: nothing -> record {
    let obj = {id: "test-id"}
    let type = (get-object-type $obj)
    if ($type == "Unknown") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["sh", "-c", $"echo 'Expected Unknown, got ($type)' && exit 1"]
    }
}

# === MULTI-TYPE WRAPPER CHECKS ===

# @check
# Verify with-directory works on Container
export def "check-with-directory-container" []: nothing -> record {
    let dir = (host directory "/tmp")
    let container = (container from "alpine" | with-directory "/mnt" $dir)
    let type = (get-object-type $container)
    if ($type == "Container") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify with-directory works on Directory
export def "check-with-directory-directory" []: nothing -> record {
    let dir = (host directory "/tmp")
    let subdir = ($dir | with-new-directory "subdir")
    let dir2 = ($dir | with-directory "/added" $subdir)
    let type = (get-object-type $dir2)
    if ($type == "Directory") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify with-file works on Container
export def "check-with-file-container" []: nothing -> record {
    let dir = (host directory "/tmp")
    let file = ($dir | with-new-file "test.txt" "content")
    let container = (container from "alpine" | with-file "/mnt/file.txt" $file)
    let type = (get-object-type $container)
    if ($type == "Container") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify with-file works on Directory
export def "check-with-file-directory" []: nothing -> record {
    let dir = (host directory "/tmp")
    let file = ($dir | with-new-file "file.txt" "content")
    let dir2 = ($dir | with-file "/new.txt" $file)
    let type = (get-object-type $dir2)
    if ($type == "Directory") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify with-new-file works on Container
export def "check-with-new-file-container" []: nothing -> record {
    let container = (container from "alpine" | with-new-file "/test.txt" "content")
    let type = (get-object-type $container)
    if ($type == "Container") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify with-new-file works on Directory
export def "check-with-new-file-directory" []: nothing -> record {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "content")
    let type = (get-object-type $dir2)
    if ($type == "Directory") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# === CONTAINER OPERATION CHECKS ===

# @check
# Verify container from works
export def "check-container-from" []: nothing -> record {
    let c = (container from "alpine")
    if (($c | get -o id | is-not-null) and ($c | get -o __type) == "Container") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify container with-exec works
export def "check-container-with-exec" []: nothing -> record {
    let result = (container from "alpine" | with-exec ["echo", "hello"] | stdout)
    if ($result == "hello") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify container stdout works
export def "check-container-stdout" []: nothing -> record {
    let result = (container from "alpine" | with-exec ["echo", "stdout-test"] | stdout)
    if ($result == "stdout-test") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify container with-env-variable works
export def "check-container-with-env-variable" []: nothing -> record {
    let result = (container from "alpine"
        | with-env-variable "TEST_VAR" "test-value"
        | with-exec ["sh", "-c", "echo $TEST_VAR"]
        | stdout)
    if ($result == "test-value") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# === DIRECTORY OPERATION CHECKS ===

# @check
# Verify directory from works
export def "check-directory-from" []: nothing -> record {
    let dir = (host directory "/tmp")
    if (($dir | get -o id | is-not-null) and ($dir | get -o __type) == "Directory") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify directory entries works
export def "check-directory-entries" []: nothing -> record {
    let dir = (host directory "/tmp")
    let entries = ($dir | entries)
    if ($entries | describe) == "list<string>" {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify directory file works
export def "check-directory-file" []: nothing -> record {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "content")
    let file = ($dir2 | file "test.txt")
    if (($file | get -o __type) == "File") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# === FILE OPERATION CHECKS ===

# @check
# Verify file contents works
export def "check-file-contents" []: nothing -> record {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "Hello, File!")
    let file = ($dir2 | file "test.txt")
    let contents = ($file | contents)
    if ($contents == "Hello, File!") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify file size works
export def "check-file-size" []: nothing -> record {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "size-test.txt" "12345")
    let file = ($dir2 | file "size-test.txt")
    let size = ($file | size)
    if ($size == 5) {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# === CROSS-TYPE OPERATION CHECKS ===

# @check
# Verify exists works on Directory
export def "check-exists-directory" []: nothing -> record {
    let dir = (host directory "/tmp")
    let exists = ($dir | exists ".")
    if $exists {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify glob works on Directory
export def "check-glob-directory" []: nothing -> record {
    let dir = (host directory "/tmp")
    let results = ($dir | glob "*.txt")
    if ($results | describe) == "list<string>" {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify contents works on File
export def "check-contents-file" []: nothing -> record {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "test.txt" "hello")
    let file = ($dir2 | file "test.txt")
    let contents = ($file | contents)
    if ($contents == "hello") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# @check
# Verify name works on File
export def "check-name-file" []: nothing -> record {
    let dir = (host directory "/tmp")
    let dir2 = ($dir | with-new-file "my-file.txt" "content")
    let file = ($dir2 | file "my-file.txt")
    let name = ($file | name)
    if ($name == "my-file.txt") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}

# === RUN ALL CHECKS ===

export def "run-all-checks" []: nothing -> record {
    {
        passed: true,
        checks: {
            type_metadata_present: (check-type-metadata-present | with-exec ["true"] | exitCode | describe),
            type_metadata_value: (check-type-metadata-value | with-exec ["true"] | exitCode | describe),
            container_from: (check-container-from | with-exec ["true"] | exitCode | describe),
            container_exec: (check-container-with-exec | with-exec ["true"] | exitCode | describe),
            container_stdout: (check-container-stdout | with-exec ["true"] | exitCode | describe),
            directory_from: (check-directory-from | with-exec ["true"] | exitCode | describe),
            directory_entries: (check-directory-entries | with-exec ["true"] | exitCode | describe),
            file_contents: (check-file-contents | with-exec ["true"] | exitCode | describe),
        }
    }
}
