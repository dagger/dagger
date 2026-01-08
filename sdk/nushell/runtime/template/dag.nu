#!/usr/bin/env nu
# Dagger API for Nushell SDK
#
# This module provides access to the Dagger API from Nushell functions.
# It uses Nushell's built-in http commands to execute GraphQL queries.
#
# The API is organized into namespaces that mirror Dagger's type system:
# - container: Container operations (from, with-exec, with-env, stdout, etc.)
# - directory: Directory operations (from, entries, file, etc.)
# - file: File operations (contents)
#
# Example usage:
#   container from "alpine:latest"
#   | container with-exec ["echo", "hello"]
#   | container stdout

# === CONTAINER NAMESPACE ===

# Create a container from a base image
export def "container from" [
    address: string  # Base image address (e.g., "alpine:latest")
]: nothing -> string {
    let query = $"query { container { from\(address: \"($address)\"\) { id } } }"
    let result = dagger-query $query
    $result.container.from.id
}

# Create an empty container
export def "container" []: nothing -> string {
    let query = "query { container { id } }"
    let result = dagger-query $query
    $result.container.id
}

# Execute a command in a container
export def "container with-exec" [
    args: list<string>  # Command and arguments to execute
]: string -> string {
    let container_id = $in
    let args_json = ($args | to json --raw)
    let query = $"query { loadContainerFromID\(id: \"($container_id)\"\) { withExec\(args: ($args_json)\) { id } } }"
    let result = dagger-query $query
    $result.loadContainerFromID.withExec.id
}

# Get stdout from a container
export def "container stdout" []: string -> string {
    let container_id = $in
    let query = $"query { loadContainerFromID\(id: \"($container_id)\"\) { stdout } }"
    let result = dagger-query $query
    $result.loadContainerFromID.stdout
}

# Get stderr from a container
export def "container stderr" []: string -> string {
    let container_id = $in
    let query = $"query { loadContainerFromID\(id: \"($container_id)\"\) { stderr } }"
    let result = dagger-query $query
    $result.loadContainerFromID.stderr
}

# Set an environment variable in a container
export def "container with-env-variable" [
    name: string   # Environment variable name
    value: string  # Environment variable value
]: string -> string {
    let container_id = $in
    let query = $"query { loadContainerFromID\(id: \"($container_id)\"\) { withEnvVariable\(name: \"($name)\", value: \"($value)\"\) { id } } }"
    let result = dagger-query $query
    $result.loadContainerFromID.withEnvVariable.id
}

# Set the working directory in a container
export def "container with-workdir" [
    path: string  # Working directory path
]: string -> string {
    let container_id = $in
    let query = $"query { loadContainerFromID\(id: \"($container_id)\"\) { withWorkdir\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    $result.loadContainerFromID.withWorkdir.id
}

# Mount a directory into a container
export def "container with-directory" [
    path: string         # Mount path in container
    directory_id: string # Directory ID to mount
]: string -> string {
    let container_id = $in
    let query = $"query { loadContainerFromID\(id: \"($container_id)\"\) { withMountedDirectory\(path: \"($path)\", source: \"($directory_id)\"\) { id } } }"
    let result = dagger-query $query
    $result.loadContainerFromID.withMountedDirectory.id
}

# Mount a file into a container
export def "container with-file" [
    path: string    # Path in container
    file_id: string # File ID to mount
]: string -> string {
    let container_id = $in
    let query = $"query { loadContainerFromID\(id: \"($container_id)\"\) { withFile\(path: \"($path)\", source: \"($file_id)\"\) { id } } }"
    let result = dagger-query $query
    $result.loadContainerFromID.withFile.id
}

# Set a secret variable in a container
export def "container with-secret-variable" [
    name: string      # Variable name
    secret_id: string # Secret ID
]: string -> string {
    let container_id = $in
    let query = $"query { loadContainerFromID\(id: \"($container_id)\"\) { withSecretVariable\(name: \"($name)\", secret: \"($secret_id)\"\) { id } } }"
    let result = dagger-query $query
    $result.loadContainerFromID.withSecretVariable.id
}

# Get a file from a container
export def "container file" [
    path: string  # File path in container
]: string -> string {
    let container_id = $in
    let query = $"query { loadContainerFromID\(id: \"($container_id)\"\) { file\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    $result.loadContainerFromID.file.id
}

# === DIRECTORY NAMESPACE ===

# Get a directory from a path on the host
export def "directory from" [
    path: string  # Path on the host filesystem
]: nothing -> string {
    let query = $"query { host { directory\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    $result.host.directory.id
}

# Get directory contents as a list
export def "directory entries" []: string -> list<string> {
    let directory_id = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory_id)\"\) { entries } }"
    let result = dagger-query $query
    $result.loadDirectoryFromID.entries
}

# Get a file from a directory
export def "directory file" [
    path: string  # File path within directory
]: string -> string {
    let directory_id = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory_id)\"\) { file\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    $result.loadDirectoryFromID.file.id
}

# === FILE NAMESPACE ===

# Get file contents as a string
export def "file contents" []: string -> string {
    let file_id = $in
    let query = $"query { loadFileFromID\(id: \"($file_id)\"\) { contents } }"
    let result = dagger-query $query
    $result.loadFileFromID.contents
}

# === SECRET NAMESPACE ===

# Get plaintext value of a secret
export def "secret plaintext" []: string -> string {
    let secret_id = $in
    let query = $"query { loadSecretFromID\(id: \"($secret_id)\"\) { plaintext } }"
    let result = dagger-query $query
    $result.loadSecretFromID.plaintext
}

# === HELPER (private) ===

# Execute a GraphQL query against the Dagger API
def dagger-query [query: string] {
    # Get the Dagger session token and port from environment
    let session_token = ($env.DAGGER_SESSION_TOKEN? | default "")
    let session_port = ($env.DAGGER_SESSION_PORT? | default "")
    
    if ($session_token == "" or $session_port == "") {
        error make {msg: "Dagger session not found. DAGGER_SESSION_TOKEN and DAGGER_SESSION_PORT must be set."}
    }
    
    # Build the GraphQL request
    let graphql_body = {query: $query}
    
    # Make the request using Nushell's http post
    let result = (
        http post 
            --content-type "application/json"
            --headers [Authorization $"Bearer ($session_token)"]
            $"http://127.0.0.1:($session_port)/query"
            $graphql_body
    )
    
    # Check for GraphQL errors
    if ($result | get -o errors | is-not-empty) {
        let error_msg = ($result.errors | get 0 | get message)
        error make {msg: $"GraphQL error: ($error_msg)"}
    }
    
    $result.data
}
