#!/usr/bin/env nu
# Dagger API - directory operations

use core.nu dagger-query

# === DIRECTORY NAMESPACE ===

# Get a directory from a path on the host
export def "directory from" [
    path: string  # Path on the host filesystem
]: nothing -> record {
    let query = $"query { host { directory\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.host.directory.id, __type: "Directory"}
}

# Create an empty directory
export def "directory new" []: nothing -> record {
    let query = "query { directory { id } }"
    let result = dagger-query $query
    {id: $result.directory.id, __type: "Directory"}
}

# Get directory contents as a list
export def "directory entries" []: record -> list<string> {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { entries } }"
    let result = dagger-query $query
    $result.loadDirectoryFromID.entries
}

# Get a file from a directory
export def "directory file" [
    path: string  # File path within directory
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { file\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.file.id, __type: "File"}
}

# Get a subdirectory
export def "directory directory" [
    path: string  # Subdirectory path
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { directory\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.directory.id, __type: "Directory"}
}

# Add a file to a directory
export def "directory with-file" [
    path: string  # Path in directory
    file: record  # File to add
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { withFile\(path: \"($path)\", source: \"($file.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.withFile.id, __type: "Directory"}
}

# Add files from another directory
export def "directory with-directory" [
    path: string    # Path in directory
    source: record  # Directory to add
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { withDirectory\(path: \"($path)\", directory: \"($source.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.withDirectory.id, __type: "Directory"}
}

# Create a new file in directory
export def "directory with-new-file" [
    path: string      # Path in directory
    contents: string  # File contents
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { withNewFile\(path: \"($path)\", contents: \"($contents)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.withNewFile.id, __type: "Directory"}
}

# Create a new directory
export def "directory with-new-directory" [
    path: string  # Path of new directory
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { withNewDirectory\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.withNewDirectory.id, __type: "Directory"}
}

# Remove a file or directory
export def "directory without" [
    path: string  # Path to remove
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { withoutDirectory\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.withoutDirectory.id, __type: "Directory"}
}

# Remove a file from directory
export def "directory without-file" [
    path: string  # Path to file to remove
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { withoutFile\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.withoutFile.id, __type: "Directory"}
}

# Remove multiple files from directory
export def "directory without-files" [
    paths: list<string>  # Paths to files to remove
]: record -> record {
    let directory = $in
    let paths_json = ($paths | to json --raw)
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { withoutFiles\(paths: ($paths_json)\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.withoutFiles.id, __type: "Directory"}
}

# Export directory to host filesystem
export def "directory export" [
    path: string  # Path on host to export to
]: record -> bool {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { export\(path: \"($path)\") } }"
    let result = dagger-query $query
    $result.loadDirectoryFromID.export
}

# Check if path exists in directory
export def "directory exists" [
    path: string  # Path to check
]: record -> bool {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { exists\(path: \"($path)\") } }"
    let result = dagger-query $query
    $result.loadDirectoryFromID.exists
}

# List files matching pattern
export def "directory glob" [
    pattern: string  # Glob pattern (e.g., "**/*.txt")
]: record -> list<string> {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { glob\(pattern: \"($pattern)\") } }"
    let result = dagger-query $query
    $result.loadDirectoryFromID.glob
}



# Forces evaluation of the directory and returns the directory
export def "directory sync" []: record -> record {
    let dir = $in
    let query = $"query { loadDirectoryFromID\(id: \"($dir.id)\"\) { sync { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.sync.id, __type: "Directory"}
}
