To run the pipeline, the API client needs to communicate with the Dagger Engine, which is responsible for accepting the query, executing it and returning the result. The `dagger run` command takes care of initializing a new local instance (or reusing a running instance) of the Dagger Engine on the host system and executing a specified command against it.

The Dagger Engine creates a unique local API endpoint for GraphQL queries for every Dagger session. This API endpoint is served by the local host at the port specified by the `DAGGER_SESSION_PORT` environment variable, and can be directly read from the environment in your client code. For example, if `DAGGER_SESSION_PORT` is set to `12345`, the API endpoint can be reached at `http://127.0.0.1:$DAGGER_SESSION_PORT/query`

:::warning
The Dagger Engine protects the exposed API with an HTTP Basic authentication token which can be retrieved from the `DAGGER_SESSION_TOKEN` variable. Treat the `DAGGER_SESSION_TOKEN` value as you would any other sensitive credential. Store it securely and avoid passing it to, or over, insecure applications and networks.
:::
