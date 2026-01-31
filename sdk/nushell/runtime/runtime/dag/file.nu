#!/usr/bin/env nu
# Dagger API - file operations

use core.nu dagger-query

# === FILE NAMESPACE ===

# Get file contents as a string
export def "file contents" []: record -> string {
    let file = $in
    let query = $"query { loadFileFromID\(id: \"($file.id)\"\) { contents } }"
    let result = dagger-query $query
    $result.loadFileFromID.contents
}

# Get file size in bytes
export def "file size" []: record -> int {
    let file = $in
    let query = $"query { loadFileFromID\(id: \"($file.id)\"\) { size } }"
    let result = dagger-query $query
    $result.loadFileFromID.size
}

# Get file name
export def "file name" []: record -> string {
    let file = $in
    let query = $"query { loadFileFromID\(id: \"($file.id)\"\) { name } }"
    let result = dagger-query $query
    $result.loadFileFromID.name
}

# Export file to host filesystem
export def "file export" [
    path: string  # Path on host to export to
]: record -> bool {
    let file = $in
    let query = $"query { loadFileFromID\(id: \"($file.id)\"\) { export\(path: \"($path)\") } }"
    let result = dagger-query $query
    $result.loadFileFromID.export
}

