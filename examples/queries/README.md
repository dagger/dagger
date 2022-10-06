# Query Examples

- [simple.graphql](./simple.graphql): Simple example of alpine+curl
  - `dagger query -f ./simple.graphql`
- [multi.graphql](./multi.graphql): Parallel operations
  - `dagger query -f ./multi.graphql`
- [git.graphql](./git.graphql): Reading a file from git
  - `dagger query -f ./git.graphql | jq -r .source.git.file`
- [docker_build.graphql](./docker_build.graphql): Builds and executes buildkit from git
  - `dagger query -f ./docker_build.graphql | jq -r .source.git.dockerbuild.exec.file`
- [params.graphql](./params.graphql)
  - `dagger query -f ./params.graphql | jq -r .source.git.file`
  - `dagger query -f ./params.graphql -set version=v0.1.0 | jq -r .source.git.file`
- [targets.graphql](./targets.graphql)
  - `dagger query -f ./targets.graphql --op test`
  - `dagger query -f ./targets.graphql --op build`
