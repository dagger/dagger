#!/usr/bin/env nu
# Dagger API - cache operations

use core.nu dagger-query

# === CACHE NAMESPACE ===

# Get or create a cache volume
export def "cache-volume" [
    name: string  # Cache volume name
]: nothing -> record {
    let query = $"query { cacheVolume\(key: \"($name)\"\) { id } }"
    let result = dagger-query $query
    {id: $result.cacheVolume.id, __type: "CacheVolume"}
}

