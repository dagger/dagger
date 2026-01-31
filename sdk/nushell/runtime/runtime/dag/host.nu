#!/usr/bin/env nu
# Dagger API - host operations

use core.nu dagger-query

# === HOST NAMESPACE ===

# Get a directory from the host
export def "host directory" [
    path: string  # Path on host
]: nothing -> record {
    let query = $"query { host { directory\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.host.directory.id, __type: "Directory"}
}

# Get a file from the host
export def "host file" [
    path: string  # Path on host
]: nothing -> record {
    let query = $"query { host { file\(path: \"($path)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.host.file.id, __type: "File"}
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
    {id: $result.host.unixSocket.id, __type: "Socket"}
}

