#!/usr/bin/env nu
# Dagger API for Nushell SDK
#
# This module provides comprehensive access to the Dagger API from Nushell functions.
# It uses Nushell's built-in http commands to execute GraphQL queries.
#
# The API is organized into namespaces that mirror Dagger's type system:
# - container: Container operations
# - directory: Directory operations  
# - file: File operations
# - host: Host system operations
# - git: Git repository operations
# - cache: Cache volume operations
# - secret: Secret operations
#
# Example usage:
#   container from "alpine:latest"
#   | container with-exec ["echo", "hello"]
#   | container stdout

# === CONTAINER NAMESPACE ===

# Create a container from a base image
export def "container from" [
    address: string  # Base image address (e.g., "alpine:latest")
]: nothing -> record {
    let query = $"query { container { from\(address: \"($address)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.container.from.id}
}

# Create an empty container
export def "container" []: nothing -> record {
    let query = "query { container { id } }"
    let result = dagger-query $query
    {id: $result.container.id}
}

# Import a container from an OCI tarball
export def "container import" [
    source: record  # File containing the OCI tarball
]: nothing -> record {
    let query = $"query { container { import\(source: \"($source.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.container.import.id}
}

# Execute a command in a container
export def "container with-exec" [
    args: list<string>  # Command and arguments to execute
]: record -> record {
    let container = $in
    let args_json = ($args | to json --raw)
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withExec\(args: ($args_json)\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withExec.id}
}

# Get stdout from a container
export def "container stdout" []: record -> string {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { stdout } }"
    let result = dagger-query $query
    $result.loadContainerFromID.stdout
}

# Get stderr from a container
export def "container stderr" []: record -> string {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { stderr } }"
    let result = dagger-query $query
    $result.loadContainerFromID.stderr
}

# Get combined stdout+stderr from a container  
export def "container combined-output" []: record -> string {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { combinedOutput } }"
    let result = dagger-query $query
    $result.loadContainerFromID.combinedOutput
}

# Get exit code of last executed command
export def "container exit-code" []: record -> int {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { exitCode } }"
    let result = dagger-query $query
    $result.loadContainerFromID.exitCode
}

# Set an environment variable in a container
export def "container with-env-variable" [
    name: string   # Environment variable name
    value: string  # Environment variable value
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withEnvVariable\(name: \"($name)\", value: \"($value)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withEnvVariable.id}
}

# Remove an environment variable from a container
export def "container without-env-variable" [
    name: string  # Environment variable name to remove
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withoutEnvVariable\(name: \"($name)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withoutEnvVariable.id}
}

# Get the value of an environment variable
export def "container env-variable" [
    name: string  # Environment variable name
]: record -> string {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { envVariable\(name: \"($name)\"\) } }"
    let result = dagger-query $query
    $result.loadContainerFromID.envVariable
}

# Get all environment variables
export def "container env-variables" []: record -> list<record> {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { envVariables { name value } } }"
    let result = dagger-query $query
    $result.loadContainerFromID.envVariables
}

# Set a label on a container
export def "container with-label" [
    name: string   # Label name
    value: string  # Label value
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withLabel\(name: \"($name)\", value: \"($value)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withLabel.id}
}

# Remove a label from a container
export def "container without-label" [
    name: string  # Label name to remove
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withoutLabel\(name: \"($name)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withoutLabel.id}
}

# Get the value of a label
export def "container label" [
    name: string  # Label name
]: record -> string {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { label\(name: \"($name)\"\) } }"
    let result = dagger-query $query
    $result.loadContainerFromID.label
}

# Get all labels
export def "container labels" []: record -> list<record> {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { labels { name value } } }"
    let result = dagger-query $query
    $result.loadContainerFromID.labels
}

# Set the working directory in a container
export def "container with-workdir" [
    path: string  # Working directory path
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withWorkdir\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withWorkdir.id}
}

# Get the working directory
export def "container workdir" []: record -> string {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { workdir } }"
    let result = dagger-query $query
    $result.loadContainerFromID.workdir
}

# Set the entrypoint
export def "container with-entrypoint" [
    args: list<string>  # Entrypoint command and arguments
]: record -> record {
    let container = $in
    let args_json = ($args | to json --raw)
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withEntrypoint\(args: ($args_json)\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withEntrypoint.id}
}

# Get the entrypoint
export def "container entrypoint" []: record -> list<string> {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { entrypoint } }"
    let result = dagger-query $query
    $result.loadContainerFromID.entrypoint
}

# Set default arguments
export def "container with-default-args" [
    args: list<string>  # Default arguments
]: record -> record {
    let container = $in
    let args_json = ($args | to json --raw)
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withDefaultArgs\(args: ($args_json)\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withDefaultArgs.id}
}

# Get default arguments
export def "container default-args" []: record -> list<string> {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { defaultArgs } }"
    let result = dagger-query $query
    $result.loadContainerFromID.defaultArgs
}

# Set the user
export def "container with-user" [
    name: string  # Username or UID
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withUser\(name: \"($name)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withUser.id}
}

# Get the user
export def "container user" []: record -> string {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { user } }"
    let result = dagger-query $query
    $result.loadContainerFromID.user
}

# Mount a directory into a container
export def "container with-directory" [
    path: string       # Mount path in container
    directory: record  # Directory to mount
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withMountedDirectory\(path: \"($path)\", source: \"($directory.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withMountedDirectory.id}
}

# Mount a file into a container
export def "container with-file" [
    path: string  # Path in container
    file: record  # File to mount
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withFile\(path: \"($path)\", source: \"($file.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withFile.id}
}

# Mount a cache volume
export def "container with-mounted-cache" [
    path: string       # Mount path in container
    cache: record      # Cache volume to mount
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withMountedCache\(path: \"($path)\", cache: \"($cache.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withMountedCache.id}
}

# Mount a temporary directory
export def "container with-mounted-temp" [
    path: string  # Mount path in container
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withMountedTemp\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withMountedTemp.id}
}

# Mount a secret as a file
export def "container with-mounted-secret" [
    path: string    # Mount path in container
    secret: record  # Secret to mount
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withMountedSecret\(path: \"($path)\", source: \"($secret.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withMountedSecret.id}
}

# Set a secret as an environment variable
export def "container with-secret-variable" [
    name: string    # Variable name
    secret: record  # Secret
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withSecretVariable\(name: \"($name)\", secret: \"($secret.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withSecretVariable.id}
}

# Remove a mount
export def "container without-mount" [
    path: string  # Mount path to remove
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withoutMount\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withoutMount.id}
}

# Get list of mount paths
export def "container mounts" []: record -> list<string> {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { mounts } }"
    let result = dagger-query $query
    $result.loadContainerFromID.mounts
}

# Get a directory from a container
export def "container directory" [
    path: string  # Directory path in container
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { directory\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.directory.id}
}

# Get a file from a container
export def "container file" [
    path: string  # File path in container
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { file\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.file.id}
}

# Expose a port
export def "container with-exposed-port" [
    port: int  # Port number to expose
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withExposedPort\(port: ($port)\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withExposedPort.id}
}

# Remove an exposed port
export def "container without-exposed-port" [
    port: int  # Port number to remove
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withoutExposedPort\(port: ($port)\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withoutExposedPort.id}
}

# Get list of exposed ports
export def "container exposed-ports" []: record -> list<int> {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { exposedPorts { port } } }"
    let result = dagger-query $query
    $result.loadContainerFromID.exposedPorts | get port
}

# Publish container to a registry
export def "container publish" [
    address: string  # Registry address (e.g., "docker.io/myorg/myimage:tag")
]: record -> string {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { publish\(address: \"($address)\"\) } }"
    let result = dagger-query $query
    $result.loadContainerFromID.publish
}

# Export container as OCI tarball
export def "container export" [
    path: string  # Path to export tarball to
]: record -> bool {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { export\(path: \"($path)\"\) } }"
    let result = dagger-query $query
    $result.loadContainerFromID.export
}

# Export container as OCI tarball to a file
export def "container as-tarball" []: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { asTarball { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.asTarball.id}
}

# Convert container to a service
export def "container as-service" []: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { asService { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.asService.id}
}

# Get container platform
export def "container platform" []: record -> string {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { platform } }"
    let result = dagger-query $query
    $result.loadContainerFromID.platform
}

# Get image reference
export def "container image-ref" []: record -> string {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { imageRef } }"
    let result = dagger-query $query
    $result.loadContainerFromID.imageRef
}

# === DIRECTORY NAMESPACE ===

# Get a directory from a path on the host
export def "directory from" [
    path: string  # Path on the host filesystem
]: nothing -> record {
    let query = $"query { host { directory\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.host.directory.id}
}

# Create an empty directory
export def "directory" []: nothing -> record {
    let query = "query { directory { id } }"
    let result = dagger-query $query
    {id: $result.directory.id}
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
    {id: $result.loadDirectoryFromID.file.id}
}

# Get a subdirectory
export def "directory directory" [
    path: string  # Subdirectory path
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { directory\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.directory.id}
}

# Add a file to a directory
export def "directory with-file" [
    path: string  # Path in directory
    file: record  # File to add
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { withFile\(path: \"($path)\", source: \"($file.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.withFile.id}
}

# Add files from another directory
export def "directory with-directory" [
    path: string    # Path in directory
    source: record  # Directory to add
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { withDirectory\(path: \"($path)\", directory: \"($source.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.withDirectory.id}
}

# Create a new file in directory
export def "directory with-new-file" [
    path: string      # Path in directory
    contents: string  # File contents
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { withNewFile\(path: \"($path)\", contents: \"($contents)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.withNewFile.id}
}

# Create a new directory
export def "directory with-new-directory" [
    path: string  # Path of new directory
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { withNewDirectory\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.withNewDirectory.id}
}

# Remove a file or directory
export def "directory without" [
    path: string  # Path to remove
]: record -> record {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { without\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadDirectoryFromID.without.id}
}

# Export directory to host filesystem
export def "directory export" [
    path: string  # Path on host to export to
]: record -> bool {
    let directory = $in
    let query = $"query { loadDirectoryFromID\(id: \"($directory.id)\"\) { export\(path: \"($path)\"\) } }"
    let result = dagger-query $query
    $result.loadDirectoryFromID.export
}

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
    let query = $"query { loadFileFromID\(id: \"($file.id)\"\) { export\(path: \"($path)\"\) } }"
    let result = dagger-query $query
    $result.loadFileFromID.export
}

# === HOST NAMESPACE ===

# Get a directory from the host
export def "host directory" [
    path: string  # Path on host
]: nothing -> record {
    let query = $"query { host { directory\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.host.directory.id}
}

# Get a file from the host
export def "host file" [
    path: string  # Path on host
]: nothing -> record {
    let query = $"query { host { file\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.host.file.id}
}

# Get an environment variable from the host
export def "host env-variable" [
    name: string  # Environment variable name
]: nothing -> string {
    let query = $"query { host { envVariable\(name: \"($name)\"\) } }"
    let result = dagger-query $query
    $result.host.envVariable
}

# Get a Unix socket from the host
export def "host unix-socket" [
    path: string  # Socket path on host
]: nothing -> record {
    let query = $"query { host { unixSocket\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.host.unixSocket.id}
}

# === GIT NAMESPACE ===

# Get a git repository
export def "git" [
    url: string  # Git repository URL
]: nothing -> record {
    let query = $"query { git\(url: \"($url)\"\) { id } }"
    let result = dagger-query $query
    {id: $result.git.id}
}

# Get a specific branch
export def "git branch" [
    name: string  # Branch name
]: record -> record {
    let repo = $in
    let query = $"query { loadGitRepositoryFromID\(id: \"($repo.id)\"\) { branch\(name: \"($name)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadGitRepositoryFromID.branch.id}
}

# Get a specific tag
export def "git tag" [
    name: string  # Tag name
]: record -> record {
    let repo = $in
    let query = $"query { loadGitRepositoryFromID\(id: \"($repo.id)\"\) { tag\(name: \"($name)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadGitRepositoryFromID.tag.id}
}

# Get a specific commit
export def "git commit" [
    hash: string  # Commit hash
]: record -> record {
    let repo = $in
    let query = $"query { loadGitRepositoryFromID\(id: \"($repo.id)\"\) { commit\(id: \"($hash)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadGitRepositoryFromID.commit.id}
}

# Get the repository tree at a ref
export def "git-ref tree" []: record -> record {
    let ref = $in
    let query = $"query { loadGitRefFromID\(id: \"($ref.id)\"\) { tree { id } } }"
    let result = dagger-query $query
    {id: $result.loadGitRefFromID.tree.id}
}

# === CACHE NAMESPACE ===

# Get or create a cache volume
export def "cache-volume" [
    name: string  # Cache volume name
]: nothing -> record {
    let query = $"query { cacheVolume\(key: \"($name)\"\) { id } }"
    let result = dagger-query $query
    {id: $result.cacheVolume.id}
}

# === SECRET NAMESPACE ===

# Get plaintext value of a secret
export def "secret plaintext" []: record -> string {
    let secret = $in
    let query = $"query { loadSecretFromID\(id: \"($secret.id)\"\) { plaintext } }"
    let result = dagger-query $query
    $result.loadSecretFromID.plaintext
}

# Create a secret from plaintext
export def "secret from-plaintext" [
    value: string  # Secret value
]: nothing -> record {
    let query = $"query { setSecret\(name: \"secret\", plaintext: \"($value)\"\) { id } }"
    let result = dagger-query $query
    {id: $result.setSecret.id}
}

# Get secret name
export def "secret name" []: record -> string {
    let secret = $in
    let query = $"query { loadSecretFromID\(id: \"($secret.id)\"\) { name } }"
    let result = dagger-query $query
    $result.loadSecretFromID.name
}

# === CHECK NAMESPACE ===

# Run a specific check
export def "check run" []: record -> record {
    let check = $in
    let query = $"query { loadCheckFromID\(id: \"($check.id)\"\) { run { id } } }"
    let result = dagger-query $query
    {id: $result.loadCheckFromID.run.id}
}

# Check if a check passed
export def "check passed" []: record -> bool {
    let check = $in
    let query = $"query { loadCheckFromID\(id: \"($check.id)\"\) { passed } }"
    let result = dagger-query $query
    $result.loadCheckFromID.passed
}

# Check if a check completed
export def "check completed" []: record -> bool {
    let check = $in
    let query = $"query { loadCheckFromID\(id: \"($check.id)\"\) { completed } }"
    let result = dagger-query $query
    $result.loadCheckFromID.completed
}

# Get check name
export def "check name" []: record -> string {
    let check = $in
    let query = $"query { loadCheckFromID\(id: \"($check.id)\"\) { name } }"
    let result = dagger-query $query
    $result.loadCheckFromID.name
}

# Get check description
export def "check description" []: record -> string {
    let check = $in
    let query = $"query { loadCheckFromID\(id: \"($check.id)\"\) { description } }"
    let result = dagger-query $query
    $result.loadCheckFromID.description
}

# Get check result emoji
export def "check result-emoji" []: record -> string {
    let check = $in
    let query = $"query { loadCheckFromID\(id: \"($check.id)\"\) { resultEmoji } }"
    let result = dagger-query $query
    $result.loadCheckFromID.resultEmoji
}

# === CHECK GROUP NAMESPACE ===

# Run all checks in a group
export def "check-group run" []: record -> record {
    let group = $in
    let query = $"query { loadCheckGroupFromID\(id: \"($group.id)\"\) { run { id } } }"
    let result = dagger-query $query
    {id: $result.loadCheckGroupFromID.run.id}
}

# List all checks in a group
export def "check-group list" []: record -> list<record> {
    let group = $in
    let query = $"query { loadCheckGroupFromID\(id: \"($group.id)\"\) { list { id name description } } }"
    let result = dagger-query $query
    $result.loadCheckGroupFromID.list | each { {id: $in.id, name: $in.name, description: $in.description} }
}

# Generate a markdown report for a check group
export def "check-group report" []: record -> string {
    let group = $in
    let query = $"query { loadCheckGroupFromID\(id: \"($group.id)\"\) { report } }"
    let result = dagger-query $query
    $result.loadCheckGroupFromID.report
}

# === MODULE NAMESPACE ===

# Get a specific check from a module
export def "module check" [
    name: string  # Check name
]: record -> record {
    let module = $in
    let query = $"query { loadModuleFromID\(id: \"($module.id)\"\) { check\(name: \"($name)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadModuleFromID.check.id}
}

# Get all checks from a module
export def "module checks" []: record -> record {
    let module = $in
    let query = $"query { loadModuleFromID\(id: \"($module.id)\"\) { checks { id } } }"
    let result = dagger-query $query
    {id: $result.loadModuleFromID.checks.id}
}

# Get a module by path
export def "module" [
    path: string  # Module path
]: nothing -> record {
    let query = $"query { module\(path: \"($path)\"\) { id } }"
    let result = dagger-query $query
    {id: $result.module.id}
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
    let errors = ($result | get -o errors)
    if ($errors != null and ($errors | is-not-empty)) {
        let error_msg = ($errors | get 0 | get message)
        error make {msg: $"GraphQL error: ($error_msg)"}
    }
    
    $result.data
}
