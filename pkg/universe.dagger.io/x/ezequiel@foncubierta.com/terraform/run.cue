package terraform

import (
  "dagger.io/dagger"
  "dagger.io/dagger/core"
  "universe.dagger.io/docker"
)

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

  // Terraform workspace
  workspace: string | *"default"

  // Environment variables
  env: [string]: string | dagger.#Secret

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
          args: cmdArgs
        }
        env: _thisEnv & {
          if workspace != "default" {
            TF_WORKSPACE: workspace
          }
        }
      }
    ]
  }

  _output: core.#Subdir & {
    input: _run.output.rootfs
    path: "/src"
  }

  // Modified Terraform files
  output: _output.output

  // -*- this -*-
  _thisEnv: env
}