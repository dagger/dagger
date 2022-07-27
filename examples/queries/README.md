# Query Examples

- [simple.graphql](./simple.graphql): Simple example of alpine+curl
  - `cloak query -q ./simple.graphql`
- [multi.graphql](./multi.graphql): Parallel operations
  - `cloak query -q ./multi.graphql`
- [git.graphql](./git.graphql): Reading a file from git
  - `cloak query -q ./git.graphql | jq -r .source.git.file`
- [docker_build.graphql](./docker_build.graphql): Builds and executes buildkit from git
  - `cloak query -q ./docker_build.graphql | jq -r .source.git.dockerbuild.exec.file`
- [params.graphql](./params.graphql)
  - `cloak query -q ./params.graphql | jq -r .source.git.file`
  - `cloak query -q ./params.graphql -set version=v0.1.0 | jq -r .source.git.file`
- [targets.graphql](./targets.graphql)
  - `cloak query -q ./targets.graphql --op test`
  - `cloak query -q ./targets.graphql --op build`
