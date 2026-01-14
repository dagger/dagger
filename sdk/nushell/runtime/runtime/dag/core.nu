#!/usr/bin/env nu
# Core Dagger API helpers

# Execute a GraphQL query against the Dagger API
export def dagger-query [query: string]: nothing -> record {
    # Get the Dagger session token and port from environment
    let session_token = ($env.DAGGER_SESSION_TOKEN? | default "")
    let session_port = ($env.DAGGER_SESSION_PORT? | default "")
    
    if ($session_token == "" or $session_port == "") {
        error make {msg: "Dagger session not found. DAGGER_SESSION_TOKEN and DAGGER_SESSION_PORT must be set."}
    }
    
    let url = $"http://127.0.0.1:($session_port)/query"
    
    let graphql_body = ({query: $query} | to json)
    let response = (
        http post 
            --content-type "application/json"
            --headers [Authorization $"Bearer ($session_token)"]
            $url
            $graphql_body
    )
    
    # Check for GraphQL errors
    let errors = ($response | get -o errors)
    if ($errors != null and ($errors | is-not-empty)) {
        let error_msg = ($errors | get 0 | get message)
        error make {msg: $"GraphQL error: ($error_msg)"}
    }
    
    $response.data
}


# Load a container by its ID
export def "load-container-from-id" [
    id: string  # Container ID
]: nothing -> record {
    {id: $id, __type: "Container"}
}

# Load a directory by its ID
export def "load-directory-from-id" [
    id: string  # Directory ID
]: nothing -> record {
    {id: $id, __type: "Directory"}
}

# Load a file by its ID
export def "load-file-from-id" [
    id: string  # File ID
]: nothing -> record {
    {id: $id, __type: "File"}
}
