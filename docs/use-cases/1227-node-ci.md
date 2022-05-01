---
slug: /1227/node-ci
displayed_sidebar: europa
---

# Basic Node CI

Dagger is incredibly useful for all kinds of complex deployment and building. In this use case we are going to focus on CI and using pre built tools to check the code we have written. It will aim to give you a basic scaffolding on which to build more complex pipelines.

## Plan of Action

The application we are building a dagger pipeline for has very simple build and publish steps.

        Information on said steps, maybe an image?

Where this pipeline is powerful is its use of pre-build tools to check the code every time new code is pushed. It uses the below tools to ensure the repo is up to scratch.

- [ESlint](https://eslint.org/)
- [Sonarcloud](https://sonarcloud.io/)
- [Audit CI](https://www.npmjs.com/package/audit-ci)
- [Gitleaks](https://github.com/zricethezav/gitleaks)
- [License Finder](https://github.com/pivotal/LicenseFinder)

It does all of this only using the Bash and Docker packages in a very simple layout.

## Breakdown of the Pipeline

### Client-Side Jobs

- First we have a step that copies the contents of the repository we are working on into an output we can then bring into our containers at a later step. We exclude the README.md and the dagger cue file so this step does not have to rerun every time we change something inconsequential.

```cue
client: {
    filesystem: {
        "./": read: {
            contents: dagger.#FS
            exclude: [
                "README.md",
                "eden.cue",
            ]
        }
    }
}
```

### Set up all dependencies

- Next we set up all the containers and tools we will need for the other steps. In this case it is a Node container for building the application and all the tools bundled into NPM, a Gitleaks container to run a secret detection scan and Sonarscanner for static code analysis. I do not imagine this will often be called with a dagger do command and is more useful as a pre-requisite for other actions.

```cue
actions: {
    deps: {
      node:
        docker.#Build & {
          steps: [
            docker.#Pull & {
              source: "index.docker.io/node"
            },
            docker.#Copy & {
              contents: client.filesystem."./".read.contents
              dest:     "./src"
            },
          ]
        }
      gitleaks:
        docker.#Build & {
          steps: [
            docker.#Pull & {
              source: "index.docker.io/zricethezav/gitleaks"
            },
            docker.#Copy & {
              contents: client.filesystem."./".read.contents
              dest:     "./src"
            },
          ]
        }
      sonarscanner:
        docker.#Build & {
          steps: [
            docker.#Pull & {
              source: "index.docker.io/sonarsource/sonar-scanner-cli"
            },
            docker.#Copy & {
              contents: client.filesystem."./".read.contents
              dest:     "/usr/src"
            },
          ]
        }
    }
```

### Build step

- Next we have a very simple build step to ensure the api still build after any changes have been made. The output of this is also used by any step that wants to run an npm command as the api is already built and all packages installed.

```cue
build: {
      bash.#Run & {
        workdir: "./src"
        input:   deps.node.output
        script: contents: #"""
          npm ci
          """#
      }
    }
```

### Static Analysis step

- In static analysis we have eslint that will go through the code and fail based on a config file that is set. We also have sonarscanner running with two variables set. The "SONAR_LOGIN" is set in Github as a secret and "GITHUB_HEAD_REF" is a built in variable from Github set as the pull request name. This names the branch on Sonarqube/Sonarcloud.

```cue
staticAnalysis: {
      lint:
        bash.#Run & {
          workdir: "./src"
          input:   build.output
          script: contents: #"""
            npx eslint --color .
            """#
        }
      sonarscanner:
        docker.#Run & {
          env: {
            GITHUB_BRANCH_NAME: client.env.GITHUB_HEAD_REF
            SONAR_LOGIN:        client.env.SONAR_LOGIN
            SONAR_HOST_URL:     "https://sonarcloud.io"
          }
          workdir: "/usr/src"
          input:   deps.sonarscanner.output
        }
    }
```