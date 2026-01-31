#!/usr/bin/env nu
# Dagger API - git operations

use core.nu dagger-query

# === GIT NAMESPACE ===

# Get a git repository
export def "git repo" [
    url: string  # Git repository URL
]: nothing -> record {
    let query = $"query { git\(url: \"($url)\"\) { id } }"
    let result = dagger-query $query
    {id: $result.git.id, __type: "GitRepository"}
}

# Get a specific branch
export def "git branch" [
    name: string  # Branch name
]: record -> record {
    let repo = $in
    let query = $"query { loadGitRepositoryFromID\(id: \"($repo.id)\"\) { branch\(name: \"($name)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadGitRepositoryFromID.branch.id, __type: "GitRef"}
}

# Get a specific tag
export def "git tag" [
    name: string  # Tag name
]: record -> record {
    let repo = $in
    let query = $"query { loadGitRepositoryFromID\(id: \"($repo.id)\"\) { tag\(name: \"($name)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadGitRepositoryFromID.tag.id, __type: "GitRef"}
}

# Get a specific commit
export def "git commit" [
    hash: string  # Commit hash
]: record -> record {
    let repo = $in
    let query = $"query { loadGitRepositoryFromID\(id: \"($repo.id)\"\) { commit\(id: \"($hash)\"\) { id } } }"
    let result = dagger-query $query
    {id: $result.loadGitRepositoryFromID.commit.id, __type: "GitRef"}
}

# Get the repository tree at a ref
export def "git-ref tree" []: record -> record {
    let ref = $in
    let query = $"query { loadGitRefFromID\(id: \"($ref.id)\"\) { tree { id } } }"
    let result = dagger-query $query
    {id: $result.loadGitRefFromID.tree.id, __type: "Directory"}
}

