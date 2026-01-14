#!/usr/bin/env nu
# Dagger API - secret operations

use core.nu dagger-query

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
    {id: $result.setSecret.id, __type: "Secret"}
}

# Get secret name
export def "secret name" []: record -> string {
    let secret = $in
    let query = $"query { loadSecretFromID\(id: \"($secret.id)\"\) { name } }"
    let result = dagger-query $query
    $result.loadSecretFromID.name
}

