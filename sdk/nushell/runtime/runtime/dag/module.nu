#!/usr/bin/env nu
# Dagger API - module operations

use core.nu dagger-query

# === MODULE NAMESPACE ===

# Get a specific check from a module
export def "module check" [
    name: string  # Check name
]: record -> record {
    let module = $in
    let query = $"query { loadModuleFromID\(id: \"($module.id)\"\) { check\(name: \"($name)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadModuleFromID.check.id, __type: "Check"}
}

# Get all checks from a module
export def "module checks" []: record -> record {
    let module = $in
    let query = $"query { loadModuleFromID\(id: \"($module.id)\"\) { checks { id } } }"
    let result = dagger-query $query
    {id: $result.loadModuleFromID.checks.id, __type: "Module"}
}

# Get a module by path
export def "module get" [
    path: string  # Module path
]: nothing -> record {
    let query = $"query { module\(path: \"($path)\"\) { id } }"
    let result = dagger-query $query
    {id: $result.module.id, __type: "Module"}
}

