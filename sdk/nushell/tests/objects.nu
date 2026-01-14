#!/usr/bin/env nu
# Object/method pattern tests - @object, @method, @field annotations with automatic inference

# === TEST HELPERS ===

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
    if ($value == null) {
        error make {msg: $"($message): expected truthy, got null"}
    }
    true
}

# === BASIC OBJECT/METHOD PATTERN ===

# @object MyService
# A simple service object
export def my-service [
    port: int = 8080
]: nothing -> record {
    {
        port: $port
        status: "stopped"
    }
}

# @method
# Start the service
export def "my-service start" [
    timeout: int = 30
]: record -> string {
    let svc = $in
    $"Starting service on port ($svc.port) with timeout ($timeout)s"
}

# @method  
# Stop the service
export def "my-service stop" []: record -> string {
    let svc = $in
    $"Stopping service on port ($svc.port)"
}

# @method
# Get the service status
export def "my-service status" []: record -> string {
    let svc = $in
    $svc.status
}

# @field
# Get the service port
export def "my-service port" []: record -> int {
    let svc = $in
    $svc.port
}

# @field
# Get the port with a custom format
export def "my-service port-formatted" []: record -> string {
    let svc = $in
    $"Port: ($svc.port)"
}

# === ANOTHER OBJECT: CacheManager ===

# @object CacheManager
# A cache manager object
export def cache-manager [
    name: string
    ttl: int = 3600
]: nothing -> record {
    {
        name: $name
        ttl: $ttl
        hits: 0
    }
}

# @method
# Get a value from cache
export def "cache-manager get" [
    key: string
]: record -> string {
    let cache = $in
    $"Getting ($key) from cache ($cache.name)"
}

# @method
# Set a value in cache
export def "cache-manager set" [
    key: string
    value: string
]: record -> string {
    let cache = $in
    $"Setting ($key) = ($value) in cache ($cache.name) with TTL ($cache.ttl)"
}

# @method
# Increment hit counter
export def "cache-manager hit" []: record -> record {
    let cache = $in
    {|$cache| hits: ($cache.hits + 1)}
}

# @field
# Get cache name
export def "cache-manager name" []: record -> string {
    let cache = $in
    $cache.name
}

# === TEST FUNCTIONS ===

# @check
export def "test-object-creation" []: nothing -> string {
    let svc = (my-service --port 3000)
    
    assert-equal ($svc.port) 3000 "port should be 3000"
    assert-equal ($svc.status) "stopped" "status should be stopped"
    "test-object-creation: PASS"
}

# @check
export def "test-method-call" []: nothing -> string {
    let svc = (my-service --port 3000)
    let result = ($svc | my-service start --timeout 60)
    
    assert-equal $result "Starting service on port 3000 with timeout 60s" "start method should return correct message"
    "test-method-call: PASS"
}

# @check
export def "test-field-access" []: nothing -> string {
    let svc = (my-service --port 3000)
    let port = ($svc | my-service port)
    
    assert-equal $port 3000 "port field should return 3000"
    "test-field-access: PASS"
}

# @check
export def "test-method-chaining" []: nothing -> string {
    let svc = (my-service --port 8080)
    let result = ($svc | my-service start | my-service status)
    
    assert-equal $result "stopped" "method chaining should work"
    "test-method-chaining: PASS"
}

# @check
export def "test-another-object" []: nothing -> string {
    let cache = (cache-manager "my-cache" --ttl 1800)
    
    assert-equal ($cache.name) "my-cache" "name should be my-cache"
    assert-equal ($cache.ttl) 1800 "ttl should be 1800"
    assert-equal ($cache.hits) 0 "hits should start at 0"
    "test-another-object: PASS"
}

# @check
export def "test-cache-methods" []: nothing -> string {
    let cache = (cache-manager "test-cache")
    let get_result = ($cache | cache-manager get "key1")
    let set_result = ($cache | cache-manager set "key2" "value2")
    
    assert-equal $get_result "Getting key1 from cache test-cache" "get method should work"
    assert-equal $set_result "Setting key2 = value2 in cache test-cache with TTL 3600" "set method should work"
    "test-cache-methods: PASS"
}

# @check
export def "test-cache-hit-increases" []: nothing -> string {
    let cache = (cache-manager "test-cache")
    let cache2 = ($cache | cache-manager hit)
    let cache3 = ($cache2 | cache-manager hit)
    
    assert-equal ($cache.hits) 0 "original cache hits should be 0"
    assert-equal ($cache2.hits) 1 "after first hit, hits should be 1"
    assert-equal ($cache3.hits) 2 "after second hit, hits should be 2"
    "test-cache-hit-increases: PASS"
}

# @check
export def "test-object-with-defaults" []: nothing -> string {
    let svc = (my-service)
    
    assert-equal ($svc.port) 8080 "default port should be 8080"
    "test-object-with-defaults: PASS"
}

# @check
export def "test-field-formatted" []: nothing -> string {
    let svc = (my-service --port 9000)
    let formatted = ($svc | my-service port-formatted)
    
    assert-equal $formatted "Port: 9000" "formatted port should include prefix"
    "test-field-formatted: PASS"
}

# === RUN ALL OBJECT/METHOD TESTS ===

# @check
export def "test-objects-all" []: nothing -> string {
    let results = [
        (test-object-creation)
        (test-method-call)
        (test-field-access)
        (test-method-chaining)
        (test-another-object)
        (test-cache-methods)
        (test-cache-hit-increases)
        (test-object-with-defaults)
        (test-field-formatted)
    ]
    
    $"Object/Method tests: ($results | length) tests passed"
}