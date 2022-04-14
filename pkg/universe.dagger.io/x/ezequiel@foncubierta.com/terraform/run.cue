package terraform

import (
  "dagger.io/dagger"
  "dagger.io/dagger/core"
  "universe.dagger.io/docker"
)

_#logLevel: "off" | "info" | "warn" | "error" | "debug" | "trace"

// Run `terraform CMD`
#Run: {
  // Hashicorp Terraform container
  containerRef: dagger.#Ref | *"hashicorp/terraform:latest"

  // Terraform source code
  source: dagger.#FS

  // Terraform command (i.e. init, plan, apply)
  cmd: string

  // Arguments for the Terraform command (i.e. -var-file, -var)
  cmdArgs: [...string] | *[]

  // Arguments set internally from other actions
  withinCmdArgs: [...string] | *[]

  // Terraform workspace
  workspace: string | *"default"

  // Data directory (i.e. ./.terraform)
  dataDir: string | *".terraform"

  // Log level
  logLevel: _#logLevel | *"off"

  // Log path
  logPath: string | *""

  // Environment variables
  env: [string]: string | dagger.#Secret

  // Environment variables set internally from other actions
  withinEnv: [string]: string | dagger.#Secret

  // Run command within a container
  _run: docker.#Build & {
    steps: [
      docker.#Pull & {
        source: containerRef
      },

      docker.#Copy & {
        dest:     "/src"
        contents: source
      },

      docker.#Run & {
        workdir: "/src"
        command: {
          name: cmd
          args: _thisCmdArgs
        }
        env: _thisEnv
      }
    ]
  }

  _afterSource: core.#Subdir & {
    input: _run.output.rootfs
    path: "/src"
  }

  // Terraform image
  outputImage: _run.output

  // Modified Terraform files
  output: _afterSource.output
  
  // -*- this -*-
  _thisCmdArgs: cmdArgs + withinCmdArgs
  _thisEnv: env & withinEnv & {
    if workspace != "default" {
      TF_WORKSPACE: workspace
    }

    if dataDir != ".terraform" {
      TF_DATA_DIR: dataDir
    }

    if logLevel != "off" {
      TF_LOG: logLevel
    }

    if logPath != "" {
      TF_LOG_PATH: logPath
    }

    TF_IN_AUTOMATION: "true"
    TF_INPUT: "0"
  }
}