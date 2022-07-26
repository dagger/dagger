# Query Examples

- [simple.graphql](./simple.graphql): Simple example of alpine+curl
  - `cloak -q ./simple.graphql`
- [multi.graphql](./multi.graphql): Parallel operations
  - `cloak -q ./multi.graphql`
- [git.graphql](./git.graphql): Reading a file from git
  - `cloak -q ./git.graphql | jq -r .source.git.file`
- [docker_build.graphql](./docker_build.graphql): Builds and executes buildkit from git
  - `cloak -q ./docker_build.graphql | jq -r .source.git.dockerfile.exec.file`
- [params.graphql](./params.graphql)
  - `cloak -q ./params.graphql | jq -r .source.git.file`
  - `cloak -q ./params.graphql -set version=v0.1.0 | jq -r .source.git.file`
- [targets.graphql](./targets.graphql)
  - `cloak -q ./targets.graphql -op test`
  - `cloak -q ./targets.graphql -op build`
