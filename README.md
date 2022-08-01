# Dagger Examples

## Dagger Quickstart Templates
Use/modify these examples to get going quickly!

If you'd like to see a new template, make a PR or add your vote to this discussion. Thank you!
https://github.com/dagger/dagger/discussions/2874

- docker
  - alpine
  - dockerfile
  - multistage
- java
  - [gradle](https://github.com/dagger/examples/tree/main/templates/java/gradle)
  - maven
- go
  - [universe](https://github.com/dagger/examples/tree/main/templates/go/universe)
- nodejs
  - npm
    - [bash](https://github.com/dagger/examples/tree/main/templates/nodejs/npm/bash)
  - yarn
    - [bash](https://github.com/dagger/examples/tree/main/templates/nodejs/yarn/bash)
    - [universe](https://github.com/dagger/examples/tree/main/templates/nodejs/yarn/universe)

#### Template guidelines

- Templates should be complete and work out of the box: `git clone...`, `cd...`, `dagger do...`
- Templates should be focused on simple build or test or deploy use case for a single platform.
- Templates should be relatively short and use Dagger core functionality or [Universe packages](https://universe.dagger.io).
