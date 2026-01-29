#!/usr/bin/env nu
# A module demonstrating @check functions in Nushell SDK

use /usr/local/lib/dag.nu *

# @check
# A passing check - returns container that exits 0
export def "passing-check" []: nothing -> record {
    container from "alpine:3" | with-exec ["sh", "-c", "exit 0"]
}

# @check
# A failing check - returns container that exits 1
export def "failing-check" []: nothing -> record {
    container from "alpine:3" | with-exec ["sh", "-c", "exit 1"]
}

# @check
# Check with file operations - verify file creation
export def "check-file-creation" []: nothing -> record {
    container from "alpine:3"
    | with-exec ["sh", "-c", "echo 'test' > /tmp/file.txt"]
    | with-exec ["test", "-f", "/tmp/file.txt"]
    | with-exec ["grep", "test", "/tmp/file.txt"]
}

# @check
# Check with directory operations
export def "check-directory-ops" []: nothing -> record {
    let dir = (
        directory
        | with-new-file "README.md" "# Test Project"
        | with-new-file "src/main.nu" "echo 'hello'"
    )
    
    container from "alpine:3"
    | with-directory "/project" $dir
    | with-exec ["test", "-f", "/project/README.md"]
    | with-exec ["test", "-f", "/project/src/main.nu"]
}

# @check
# Check with environment variables
export def "check-with-env" []: nothing -> record {
    container from "alpine:3"
    | with-env-variable "TEST_VAR" "test_value"
    | with-exec ["sh", "-c", "test \"$TEST_VAR\" = \"test_value\""]
}
