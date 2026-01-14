#!/usr/bin/env nu
# Dagger API - container operations

use core.nu dagger-query

# === CONTAINER NAMESPACE ===

# Create a container from a base image
export def "container from" [
    address: string  # Base image address (e.g., "alpine:latest")
]: nothing -> record {
    let query = $"query { container { from\(address: \"($address)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.container.from.id, __type: "Container"}
}

# Create an empty container
export def "container new" []: nothing -> record {
    let query = "query { container { id } }"
    let result = dagger-query $query
    {id: $result.container.id, __type: "Container"}
}

# Import a container from an OCI tarball
export def "container import" [
    source: record  # File containing the OCI tarball
]: nothing -> record {
    let query = $"query { container { import\(source: \"($source.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.container.import.id, __type: "Container"}
}

# Execute a command in a container
export def "container with-exec" [
    args: list<string>  # Command and arguments to execute
]: record -> record {
    let container = $in
    let args_json = ($args | to json --raw)
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withExec\(args: ($args_json)\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withExec.id, __type: "Container"}
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
    {id: $result.loadContainerFromID.withEnvVariable.id, __type: "Container"}
}

# Remove an environment variable from a container
export def "container without-env-variable" [
    name: string  # Environment variable name to remove
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withoutEnvVariable\(name: \"($name)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withoutEnvVariable.id, __type: "Container"}
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
    {id: $result.loadContainerFromID.withLabel.id, __type: "Container"}
}

# Remove a label from a container
export def "container without-label" [
    name: string  # Label name to remove
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withoutLabel\(name: \"($name)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withoutLabel.id, __type: "Container"}
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
    {id: $result.loadContainerFromID.withWorkdir.id, __type: "Container"}
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
    {id: $result.loadContainerFromID.withEntrypoint.id, __type: "Container"}
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
    {id: $result.loadContainerFromID.withDefaultArgs.id, __type: "Container"}
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
    {id: $result.loadContainerFromID.withUser.id, __type: "Container"}
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
    {id: $result.loadContainerFromID.withMountedDirectory.id, __type: "Container"}
}

# Mount a file into a container
export def "container with-file" [
    path: string  # Path in container
    file: record  # File to mount
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withFile\(path: \"($path)\", source: \"($file.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withFile.id, __type: "Container"}
}

# Mount a cache volume
export def "container with-mounted-cache" [
    path: string       # Mount path in container
    cache: record      # Cache volume to mount
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withMountedCache\(path: \"($path)\", cache: \"($cache.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withMountedCache.id, __type: "Container"}
}

# Mount a temporary directory
export def "container with-mounted-temp" [
    path: string  # Mount path in container
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withMountedTemp\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withMountedTemp.id, __type: "Container"}
}

# Mount a secret as a file
export def "container with-mounted-secret" [
    path: string    # Mount path in container
    secret: record  # Secret to mount
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withMountedSecret\(path: \"($path)\", source: \"($secret.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withMountedSecret.id, __type: "Container"}
}

# Set a secret as an environment variable
export def "container with-secret-variable" [
    name: string    # Variable name
    secret: record  # Secret
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withSecretVariable\(name: \"($name)\", secret: \"($secret.id)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withSecretVariable.id, __type: "Container"}
}

# Remove a mount
export def "container without-mount" [
    path: string  # Mount path to remove
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withoutMount\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withoutMount.id, __type: "Container"}
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
    {id: $result.loadContainerFromID.directory.id, __type: "Directory"}
}

# Get a file from a container
export def "container file" [
    path: string  # File path in container
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { file\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.file.id, __type: "File"}
}

# Expose a port
export def "container with-exposed-port" [
    port: int  # Port number to expose
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withExposedPort\(port: ($port)\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withExposedPort.id, __type: "Container"}
}

# Remove an exposed port
export def "container without-exposed-port" [
    port: int  # Port number to remove
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { withoutExposedPort\(port: ($port)\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.withoutExposedPort.id, __type: "Container"}
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
    {id: $result.loadContainerFromID.asTarball.id, __type: "File"}
}

# Convert container to a service
export def "container as-service" []: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { asService { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.asService.id, __type: "Service"}
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


# Check if path exists in container
export def "container exists" [
    path: string  # Path to check
]: record -> bool {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { exists\(path: \"($path)\") } }"
    let result = dagger-query $query
    $result.loadContainerFromID.exists
}

# Get file/directory info
export def "container stat" [
    path: string  # Path to file or directory
]: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"($container.id)\"\) { stat\(path: \"($path)\") { size mode mtime name } } }"
    let result = dagger-query $query
    $result.loadContainerFromID.stat
}


# Forces evaluation of the container and returns the container
# Useful to ensure a container has been executed before continuing
export def "container sync" []: record -> record {
    let container = $in
    let query = $"query { loadContainerFromID\(id: \"$($container.id)\"\) { sync { id } } }"
    let result = dagger-query $query
    {id: $result.loadContainerFromID.sync.id, __type: "Container"}
}
