#!/usr/bin/env nu
# Dagger API - check operations

use core.nu dagger-query

# === CHECK NAMESPACE ===

# Run a specific check
export def "check run" []: record -> record {
    let check = $in
    let query = $"query { loadCheckFromID\(id: \"($check.id)\"\) { run { id } } }"
    let result = dagger-query $query
    {id: $result.loadCheckFromID.run.id, __type: "Check"}
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

