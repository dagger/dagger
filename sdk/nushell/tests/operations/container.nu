#!/usr/bin/env nu
# Container operation tests

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

# === BASIC CONTAINER OPERATIONS ===

# @check
export def "test-container-from" []: nothing -> string {
    let c = (container from "alpine")
    assert-truthy ($c | get -i id | is-not-null) "container should have id"
    assert-equal ($c | get -i __type) "Container" "container should have __type: Container"
    "test-container-from: PASS"
}

# @check
export def "test-container-new" []: nothing -> string {
    let c = (container new)
    assert-truthy ($c | get -i id | is-not-null) "new container should have id"
    "test-container-new: PASS"
}

# @check
export def "test-container-with-exec" []: nothing -> string {
    let result = (container from "alpine" | with-exec ["echo", "hello"] | stdout)
    assert-equal $result "hello" "with-exec should run command and return stdout"
    "test-container-with-exec: PASS"
}

# @check
export def "test-container-stdout" []: nothing -> string {
    let result = (container from "alpine" | with-exec ["echo", "stdout-test"] | stdout)
    assert-equal $result "stdout-test" "stdout should return command output"
    "test-container-stdout: PASS"
}

# @check
export def "test-container-stderr" []: nothing -> string {
    let result = (container from "alpine" | with-exec ["sh", "-c", "echo error >&2"] | stderr)
    assert-equal $result "error" "stderr should return error output"
    "test-container-stderr: PASS"
}

# @check
export def "test-container-combined-output" []: nothing -> string {
    let result = (container from "alpine" | with-exec ["sh", "-c", "echo out; echo err >&2"] | combined-output)
    assert-truthy ($result | str contains "out") "combined-output should contain stdout"
    assert-truthy ($result | str contains "err") "combined-output should contain stderr"
    "test-container-combined-output: PASS"
}

# @check
export def "test-container-exit-code" []: nothing -> string {
    let result = (container from "alpine" | with-exec ["sh", "-c", "exit 42"] | exit-code)
    assert-equal $result 42 "exit-code should return command exit code"
    "test-container-exit-code: PASS"
}

# === ENVIRONMENT VARIABLE OPERATIONS ===

# @check
export def "test-container-with-env-variable" []: nothing -> string {
    let result = (container from "alpine"
        | with-env-variable "TEST_VAR" "test-value"
        | with-exec ["sh", "-c", "echo $TEST_VAR"]
        | stdout)
    assert-equal $result "test-value" "with-env-variable should set env var"
    "test-container-with-env-variable: PASS"
}

# @check
export def "test-container-without-env-variable" []: nothing -> string {
    let result = (container from "alpine"
        | with-env-variable "TO_REMOVE" "value"
        | without-env-variable "TO_REMOVE"
        | with-exec ["sh", "-c", "echo $TO_REMOVE"]
        | stdout)
    assert-equal $result "" "without-env-variable should remove env var"
    "test-container-without-env-variable: PASS"
}

# @check
export def "test-container-env-variable" []: nothing -> string {
    let result = (container from "alpine"
        | with-env-variable "GET_ME" "found-it"
        | env-variable "GET_ME")
    assert-equal $result "found-it" "env-variable should return value"
    "test-container-env-variable: PASS"
}

# @check
export def "test-container-env-variables" []: nothing -> string {
    let result = (container from "alpine"
        | with-env-variable "VAR1" "val1"
        | with-env-variable "VAR2" "val2"
        | env-variables)
    assert-truthy ($result | where $it.name == "VAR1" | is-not-empty) "env-variables should include VAR1"
    assert-truthy ($result | where $it.name == "VAR2" | is-not-empty) "env-variables should include VAR2"
    "test-container-env-variables: PASS"
}

# === LABEL OPERATIONS ===

# @check
export def "test-container-with-label" []: nothing -> string {
    let result = (container from "alpine"
        | with-label "app" "myapp"
        | label "app")
    assert-equal $result "myapp" "with-label should set label"
    "test-container-with-label: PASS"
}

# @check
export def "test-container-without-label" []: nothing -> string {
    let result = (container from "alpine"
        | with-label "to-remove" "value"
        | without-label "to-remove"
        | label "to-remove")
    assert-equal $result null "without-label should remove label"
    "test-container-without-label: PASS"
}

# @check
export def "test-container-labels" []: nothing -> string {
    let result = (container from "alpine"
        | with-label "label1" "val1"
        | with-label "label2" "val2"
        | labels)
    assert-truthy ($result | where $it.name == "label1" | is-not-empty) "labels should include label1"
    assert-truthy ($result | where $it.name == "label2" | is-not-empty) "labels should include label2"
    "test-container-labels: PASS"
}

# === WORKDIR OPERATIONS ===

# @check
export def "test-container-with-workdir" []: nothing -> string {
    let result = (container from "alpine"
        | with-workdir "/app"
        | with-exec ["pwd"]
        | stdout)
    assert-equal $result "/app" "with-workdir should change directory"
    "test-container-with-workdir: PASS"
}

# @check
export def "test-container-workdir" []: nothing -> string {
    let result = (container from "alpine"
        | with-workdir "/test/path"
        | workdir)
    assert-equal $result "/test/path" "workdir should return current directory"
    "test-container-workdir: PASS"
}

# === ENTRYPOINT OPERATIONS ===

# @check
export def "test-container-with-entrypoint" []: nothing -> string {
    let result = (container from "alpine"
        | with-entrypoint ["echo"]
        | with-exec ["entry-test"]
        | stdout)
    assert-equal $result "entry-test" "with-entrypoint should change entrypoint"
    "test-container-with-entrypoint: PASS"
}

# @check
export def "test-container-entrypoint" []: nothing -> string {
    let result = (container from "alpine"
        | with-entrypoint ["cat", "/etc/hostname"]
        | entrypoint)
    assert-equal $result ["cat", "/etc/hostname"] "entrypoint should return current entrypoint"
    "test-container-entrypoint: PASS"
}

# === USER OPERATIONS ===

# @check
export def "test-container-with-user" []: nobody -> string {
    let result = (container from "alpine"
        | with-user "nobody"
        | user)
    assert-equal $result "nobody" "with-user should change user"
    "test-container-with-user: PASS"
}

# === EXPOSED PORT OPERATIONS ===

# @check
export def "test-container-with-exposed-port" []: nothing -> string {
    let result = (container from "alpine"
        | with-exposed-port 8080
        | exposed-ports)
    assert-truthy ($result | where $it.port == 8080 | is-not-empty) "with-exposed-port should add port"
    "test-container-with-exposed-port: PASS"
}

# @check
export def "test-container-without-exposed-port" []: nothing -> string {
    let result = (container from "alpine"
        | with-exposed-port 8080
        | with-exposed-port 9090
        | without-exposed-port 9090
        | exposed-ports)
    assert-truthy ($result | where $it.port == 8080 | is-not-empty) "should still have port 8080"
    assert-equal ($result | where $it.port == 9090 | length) 0 "should not have port 9090"
    "test-container-without-exposed-port: PASS"
}

# === MOUNT OPERATIONS ===

# @check
export def "test-container-with-mounted-directory" []: nothing -> string {
    let host_dir = (host directory "/tmp")
    let result = (container from "alpine"
        | with-mounted-directory "/mnt" $host_dir
        | with-exec ["ls", "/mnt"]
        | stdout)
    assert-truthy ($result | str contains ".") "mounted directory should be accessible"
    "test-container-with-mounted-directory: PASS"
}

# @check
export def "test-container-with-mounted-cache" []: nothing -> string {
    let cache = (cache-volume "test-cache")
    let result = (container from "alpine"
        | with-mounted-cache "/cache" $cache
        | mounts)
    assert-truthy ($result | where $it == "/cache" | is-not-empty) "should have cache mount"
    "test-container-with-mounted-cache: PASS"
}

# @check
export def "test-container-without-mount" []: nothing -> string {
    let cache = (cache-volume "test-cache2")
    let container = (container from "alpine"
        | with-mounted-cache "/to-remove" $cache)
    let result = ($container | without-mount "/to-remove" | mounts)
    assert-equal ($result | where $it == "/to-remove" | length) 0 "mount should be removed"
    "test-container-without-mount: PASS"
}

# === RUN ALL CONTAINER TESTS ===

# @check
export def "test-container-all" []: nothing -> string {
    let results = [
        (test-container-from)
        (test-container-new)
        (test-container-with-exec)
        (test-container-stdout)
        (test-container-stderr)
        (test-container-combined-output)
        (test-container-exit-code)
        (test-container-with-env-variable)
        (test-container-without-env-variable)
        (test-container-env-variable)
        (test-container-env-variables)
        (test-container-with-label)
        (test-container-without-label)
        (test-container-labels)
        (test-container-with-workdir)
        (test-container-workdir)
        (test-container-with-entrypoint)
        (test-container-entrypoint)
        (test-container-with-user)
        (test-container-with-exposed-port)
        (test-container-without-exposed-port)
        (test-container-with-mounted-directory)
        (test-container-with-mounted-cache)
        (test-container-without-mount)
    ]
    
    $"Container tests: ($results | length) tests passed"
}
