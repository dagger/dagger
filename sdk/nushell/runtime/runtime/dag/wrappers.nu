#!/usr/bin/env nu
# Smart pipeline wrappers for Dagger API
#
# These wrappers automatically detect the object type from the pipeline
# and dispatch to the appropriate namespace function.
#
# This allows for clean pipeline syntax without repeating the namespace:
#   container from "alpine"
#     | with-exec ["echo", "hello"]    # Automatically knows it's a Container
#     | stdout                          # Clean and concise!
#
# NAMING CONVENTIONS:
# Most wrappers match the API function names (e.g., "with-exec", "stdout").
# However, some wrappers deviate to avoid conflicts with Nushell builtins/external commands:
#
#   get-file      (not "file")       - Avoids /usr/bin/file external command
#   glob-files    (not "glob")       - Avoids Nushell's builtin glob command
#   path-exists   (not "exists")     - More descriptive and explicit
#   get-directory (not "directory")  - Mirrors get-file for consistency
#
# These names prevent shadowing issues where the builtin/external command would
# be called instead of our wrapper, causing "Command does not support record input" errors.

# Note: This module expects to be imported after other namespace modules
# The namespace functions (container.nu, directory.nu, file.nu, etc.) are
# imported via mod.nu and should be available in the calling scope

# Helper to get object type from metadata
def get-object-type [obj: record]: nothing -> string {
    $obj | get -o __type | default "Unknown"
}


# === CONTAINER OPERATIONS ===

export def "with-exec" [args: list<string>]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-exec $args }
        _ => { error make {msg: "with-exec only works on Container objects"} }
    }
}

export def "stdout" []: record -> string {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container stdout }
        _ => { error make {msg: "stdout only works on Container objects"} }
    }
}

export def "stderr" []: record -> string {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container stderr }
        _ => { error make {msg: "stderr only works on Container objects"} }
    }
}

export def "with-env-variable" [name: string, value: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-env-variable $name $value }
        _ => { error make {msg: "with-env-variable only works on Container objects"} }
    }
}

export def "without-env-variable" [name: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container without-env-variable $name }
        _ => { error make {msg: "without-env-variable only works on Container objects"} }
    }
}

export def "with-workdir" [path: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-workdir $path }
        _ => { error make {msg: "with-workdir only works on Container objects"} }
    }
}

export def "with-entrypoint" [args: list<string>]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-entrypoint $args }
        _ => { error make {msg: "with-entrypoint only works on Container objects"} }
    }
}

export def "with-default-args" [args: list<string>]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-default-args $args }
        _ => { error make {msg: "with-default-args only works on Container objects"} }
    }
}

export def "with-user" [name: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-user $name }
        _ => { error make {msg: "with-user only works on Container objects"} }
    }
}

export def "with-label" [name: string, value: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-label $name $value }
        _ => { error make {msg: "with-label only works on Container objects"} }
    }
}

export def "without-label" [name: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container without-label $name }
        _ => { error make {msg: "without-label only works on Container objects"} }
    }
}

export def "with-exposed-port" [port: int]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-exposed-port $port }
        _ => { error make {msg: "with-exposed-port only works on Container objects"} }
    }
}

export def "without-exposed-port" [port: int]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container without-exposed-port $port }
        _ => { error make {msg: "without-exposed-port only works on Container objects"} }
    }
}

export def "with-mounted-directory" [path: string, source: record]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-mounted-directory $path $source }
        _ => { error make {msg: "with-mounted-directory only works on Container objects"} }
    }
}

export def "with-mounted-file" [path: string, source: record]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-mounted-file $path $source }
        _ => { error make {msg: "with-mounted-file only works on Container objects"} }
    }
}

export def "with-mounted-cache" [path: string, cache: record]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-mounted-cache $path $cache }
        _ => { error make {msg: "with-mounted-cache only works on Container objects"} }
    }
}

export def "with-mounted-secret" [path: string, source: record]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-mounted-secret $path $source }
        _ => { error make {msg: "with-mounted-secret only works on Container objects"} }
    }
}

export def "without-mount" [path: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container without-mount $path }
        _ => { error make {msg: "without-mount only works on Container objects"} }
    }
}

export def "with-directory" [path: string, directory: record]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-directory $path $directory }
        "Directory" => { $obj | directory with-directory $path $directory }
        _ => { error make {msg: "with-directory works on Container or Directory objects"} }
    }
}

export def "without-directory" [path: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container without-directory $path }
        "Directory" => { $obj | directory without $path }
        _ => { error make {msg: "without-directory works on Container or Directory objects"} }
    }
}

export def "with-file" [path: string, source: record]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-file $path $source }
        "Directory" => { $obj | directory with-file $path $source }
        _ => { error make {msg: "with-file works on Container or Directory objects"} }
    }
}

export def "without-file" [path: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container without-file $path }
        "Directory" => { $obj | directory without-file $path }
        _ => { error make {msg: "without-file works on Container or Directory objects"} }
    }
}

export def "with-new-file" [path: string, contents: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-new-file $path $contents }
        "Directory" => { $obj | directory with-new-file $path $contents }
        _ => { error make {msg: "with-new-file works on Container or Directory objects"} }
    }
}

export def "with-secret-variable" [name: string, secret: record]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-secret-variable $name $secret }
        _ => { error make {msg: "with-secret-variable only works on Container objects"} }
    }
}

export def "with-service-binding" [alias: string, service: record]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-service-binding $alias $service }
        _ => { error make {msg: "with-service-binding only works on Container objects"} }
    }
}

export def "without-service-binding" [alias: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container without-service-binding $alias }
        _ => { error make {msg: "without-service-binding only works on Container objects"} }
    }
}

export def "with-unix-socket" [path: string, source: record]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container with-unix-socket $path $source }
        _ => { error make {msg: "with-unix-socket only works on Container objects"} }
    }
}

export def "without-unix-socket" [path: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container without-unix-socket $path }
        _ => { error make {msg: "without-unix-socket only works on Container objects"} }
    }
}

export def "as-service" []: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container as-service }
        _ => { error make {msg: "as-service only works on Container objects"} }
    }
}

export def "publish" [address: string]: record -> string {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container publish $address }
        _ => { error make {msg: "publish only works on Container objects"} }
    }
}

export def "export" [path: string]: record -> bool {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container export $path }
        "File" => { $obj | file export $path }
        "Directory" => { $obj | directory export $path }
        _ => { error make {msg: $"export works on Container, File, or Directory objects, got (get-object-type $obj)"} }
    }
}

# === DIRECTORY OPERATIONS ===

export def "entries" []: record -> list<string> {
    let obj = $in
    match (get-object-type $obj) {
        "Directory" => { $obj | directory entries }
        _ => { error make {msg: "entries only works on Directory objects"} }
    }
}

export def "with-new-directory" [path: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Directory" => { $obj | directory with-new-directory $path }
        _ => { error make {msg: "with-new-directory only works on Directory objects"} }
    }
}

# Get a file from a directory or container
# Note: Named "get-file" instead of "file" to avoid conflict with /usr/bin/file external command
export def "get-file" [path: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Directory" => { $obj | directory file $path }
        "Container" => { $obj | container file $path }
        _ => { error make {msg: "get-file only works on Directory or Container objects"} }
    }
}

# Get a subdirectory from a directory
# Note: Named "get-directory" instead of "directory" for consistency with get-file
export def "get-directory" [path: string]: record -> record {
    let obj = $in
    match (get-object-type $obj) {
        "Directory" => { $obj | directory directory $path }
        _ => { error make {msg: "get-directory only works on Directory objects"} }
    }
}

# === FILE OPERATIONS ===

export def "contents" []: record -> string {
    let obj = $in
    match (get-object-type $obj) {
        "File" => { $obj | file contents }
        _ => { error make {msg: "contents only works on File objects"} }
    }
}

export def "size" []: record -> int {
    let obj = $in
    match (get-object-type $obj) {
        "File" => { $obj | file size }
        _ => { error make {msg: "size only works on File objects"} }
    }
}

# Get file name
export def "name" []: record -> string {
    let obj = $in
    match (get-object-type $obj) {
        "File" => { $obj | file name }
        _ => { error make {msg: "name only works on File objects"} }
    }
}

# === MULTI-TYPE OPERATIONS ===

# Check if path exists (works on Container and Directory)
# Note: Named "path-exists" instead of "exists" for clarity and explicitness
export def "path-exists" [path: string]: record -> bool {
    let obj = $in
    match (get-object-type $obj) {
        "Container" => { $obj | container exists $path }
        "Directory" => { $obj | directory exists $path }
        _ => { error make {msg: "path-exists works on Container or Directory objects"} }
    }
}

# List files matching pattern (works on Directory)
# Note: Named "glob-files" instead of "glob" to avoid conflict with Nushell's builtin glob command
export def "glob-files" [pattern: string]: record -> list<string> {
    let obj = $in
    match (get-object-type $obj) {
        "Directory" => { $obj | directory glob $pattern }
        _ => { error make {msg: "glob-files only works on Directory objects"} }
    }
}


# Forces evaluation and returns the object
# Works on Container, Directory, File objects
export def "sync" []: record -> record {
    let obj = $in
    let obj_type = (get-object-type $obj)
    
    match $obj_type {
        "Container" => { $obj | container sync }
        "Directory" => { $obj | directory sync }
        _ => { error make {msg: $"sync not supported for ($obj_type)"} }
    }
}
