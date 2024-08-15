
module.exports = {
  "current": [
    {
      "type": "doc",
      "id": "index",
      "label": "Introduction"
    },
    {
      "type": "category",
      "label": "Features",
      "link": {
        "type": "doc",
        "id": "features/index"
      },
      "items": [
        "features/programmable-pipelines",
        "features/reusable-modules",
        "features/caching",
        "features/debugging",
        "features/services",
        "features/secrets",
        "features/visualization",
      ]
    },
    {
      "type": "doc",
      "label": "Installation",
      "id": "install"
    },
    {
      "type": "category",
      "label": "Features",
      "items": [
        "features/programmable-builds",
        "features/caching",
        "features/debugging",
        "features/services",
        "features/secrets",
        "features/visualization",
        "features/language-interoperability",
      ]
    },
    {
      "type": "category",
      "label": "Quickstart",
      "link": {
        "type": "doc",
        "id": "quickstart/index"
      },
      "items": [
        "quickstart/cli",
        "quickstart/daggerize",
        "quickstart/env",
        "quickstart/test",
        "quickstart/build",
        "quickstart/publish",
        "quickstart/simplify",
        "quickstart/conclusion"
      ]
    },
    {
      "type": "doc",
      "label": "Adopting Dagger",
      "id": "adopting"
    },
    {
      "type": "doc",
      "label": "Cookbook",
      "id": "cookbook/cookbook"
    },
    {
      "type": "category",
      "label": "Integrations",
      "link": {
        "type": "doc",
        "id": "integrations/index"
      },
      "collapsible": true,
      "collapsed": true,
      "items": [
        {
          "type": "doc",
          "id": "integrations/argo-workflows"
        },
        {
          "type": "doc",
          "id": "integrations/aws-codebuild"
        },
        {
          "type": "doc",
          "id": "integrations/azure-pipelines"
        },
        {
          "type": "doc",
          "id": "integrations/circleci"
        },
        {
          "type": "doc",
          "id": "integrations/github"
        },
        {
          "type": "doc",
          "id": "integrations/gitlab"
        },
        {
          "type": "doc",
          "id": "integrations/google-cloud-run"
        },
        {
          "type": "doc",
          "id": "integrations/java"
        },
        {
          "type": "doc",
          "id": "integrations/jenkins"
        },
        {
          "type": "doc",
          "id": "integrations/kubernetes"
        },
        {
          "type": "doc",
          "id": "integrations/nerdctl"
        },
        {
          "type": "doc",
          "id": "integrations/openshift"
        },
        {
          "type": "doc",
          "id": "integrations/php"
        },
        {
          "type": "doc",
          "id": "integrations/podman"
        },
        {
          "type": "doc",
          "id": "integrations/tekton"
        }
      ]
    },
    {
      "type": "category",
      "label": "Dagger API",
      "link": {
        "type": "doc",
        "id": "api/index"
      },
      "collapsible": true,
      "collapsed": true,
      "items": [
        "api/calling",
        "api/extending",
        "api/types",
        {
          "type": "category",
          "label": "API Documentation",
          "collapsible": true,
          "collapsed": true,
          "items": [
            "api/basics",
            "api/ide-integration",
            "api/arguments",
            "api/return-values",
            "api/chaining",
            "api/debugging",
            "api/documentation",
            "api/secrets",
            "api/services",
            "api/cache-volumes",
            "api/host-resources",
            "api/remote-resources",
            "api/error-handling",
            "api/execution-environment",
            "api/structure-packaging",
            "api/entrypoint-function",
            "api/custom-types",
            "api/state-functions",
            "api/enumerations",
            "api/interfaces",
            "api/runtimes",
            "api/language-dependencies",
            {
              "type": "link",
              "label": "Go SDK Reference",
              "href": "https://pkg.go.dev/dagger.io/dagger"
            },
            {
              "type": "link",
              "label": "Python SDK Reference",
              "href": "https://dagger-io.readthedocs.org/"
            },
            {
              "type": "doc",
              "label": "TypeScript SDK Reference",
              "id": "reference/typescript/modules"
            },

          ],
        },
        {
          "type": "doc",
          "label": "CLI Reference",
          "id": "reference/cli"
        },
        "api/internals",
      ]
    },
    /*
    {
      "type": "category",
      "label": "Manual",
      "link": {
        "type": "doc",
        "id": "manual/index"
      },
      "collapsible": true,
      "collapsed": true,
      "items": [
        {
          "type": "doc",
          "id": "manual/components"
        },
        {
          "type": "category",
          "label": "Dagger CLI",
          "link": {
            "type": "doc",
            "id": "manual/cli/index"
          },
          "collapsible": true,
          "collapsed": true,
            items: [
              {
                "type": "doc",
                "id": "manual/cli/basics"
              },
              {
                "type": "doc",
                "id": "manual/cli/chaining"
              },
              {
                "type": "doc",
                "id": "manual/cli/tui"
              },
              {
                "type": "doc",
                "id": "manual/cli/uninstall"
              },
              {
                "type": "doc",
                "label": "CLI Reference",
                "id": "reference/cli"
              },
            ],
        },
        {
          "type": "category",
          "label": "Dagger Functions",
          "link": {
            "type": "doc",
            "id": "manual/functions/index"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "doc",
              "id": "manual/functions/basics"
            },
            {
              "type": "doc",
              "id": "manual/functions/ide-integration"
            },
            {
              "type": "doc",
              "id": "manual/functions/arguments"
            },
            {
              "type": "doc",
              "id": "manual/functions/return-values"
            },
            {
              "type": "doc",
              "id": "manual/functions/chaining"
            },
            {
              "type": "doc",
              "id": "manual/functions/debugging"
            },
            {
              "type": "doc",
              "id": "manual/functions/visualization"
            },
            {
              "type": "doc",
              "id": "manual/functions/documentation"
            },
            {
              "type": "doc",
              "id": "manual/functions/secrets"
            },
            {
              "type": "doc",
              "id": "manual/functions/services"
            },
            {
              "type": "doc",
              "id": "manual/functions/cache-volumes"
            },
            {
              "type": "doc",
              "id": "manual/functions/host-resources"
            },
            {
              "type": "doc",
              "id": "manual/functions/remote-resources"
            },
            {
              "type": "doc",
              "id": "manual/functions/error-handling"
            },
            {
              "type": "doc",
              "id": "manual/functions/publish"
            },
            {
              "type": "category",
              "label": "Advanced Topics",
              "collapsible": true,
              "collapsed": true,
              "items": [
                {
                  "type": "doc",
                  "id": "manual/functions/execution-environment"
                },
                {
                  "type": "doc",
                  "id": "manual/functions/structure-packaging"
                },
                {
                  "type": "doc",
                  "id": "manual/functions/entrypoint-function"
                },
                {
                  "type": "doc",
                  "id": "manual/functions/custom-types"
                },
                {
                  "type": "doc",
                  "id": "manual/functions/state-functions"
                },
                {
                  "type": "doc",
                  "id": "manual/functions/enumerations"
                },
                {
                  "type": "doc",
                  "id": "manual/functions/interfaces"
                },
                {
                  "type": "doc",
                  "id": "manual/functions/runtimes"
                },
                {
                  "type": "doc",
                  "id": "manual/functions/language-dependencies"
                },
              ],
            },
            {
              "type": "category",
              "label": "Reference",
              "collapsible": true,
              "collapsed": true,
              "items": [
                {
                  "type": "link",
                  "label": "Go SDK Reference",
                  "href": "https://pkg.go.dev/dagger.io/dagger"
                },
                {
                  "type": "link",
                  "label": "Python SDK Reference",
                  "href": "https://dagger-io.readthedocs.org/"
                },
                {
                  "type": "doc",
                  "label": "TypeScript SDK Reference",
                  "id": "reference/typescript/modules"
                },
              ]
            },
          ],
        },
        {
          "type": "category",
          "label": "Dagger Cloud",
          "link": {
            "type": "doc",
            "id": "manual/cloud/index"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "doc",
              "id": "manual/cloud/configuration"
            },
            {
              "type": "doc",
              "id": "manual/cloud/traces"
            },
            {
              "type": "doc",
              "id": "manual/cloud/caching"
            },
            {
              "type": "doc",
              "id": "manual/cloud/organizations"
            },
            {
              "type": "doc",
              "id": "manual/cloud/roles-permissions"
            },
          ],
        },
        {
          "type": "category",
          "label": "Dagger API",
          "link": {
            "type": "doc",
            "id": "manual/api/index"
          },
          "collapsible": true,
          "collapsed": true,
            items: [
              {
                "type": "doc",
                "id": "manual/api/queries"
              },
              {
                "type": "doc",
                "id": "manual/api/state-representation"
              },
              {
                "type": "doc",
                "id": "manual/api/lazy-evaluation"
              },
              {
                "type": "doc",
                "id": "manual/api/types"
              },
              {
                "type": "link",
                "label": "API Reference",
                "href": "https://docs.dagger.io/api/reference"
              },
            ],
        },
        {
          "type": "category",
          "label": "Dagger Engine",
          "link": {
            "type": "doc",
            "id": "manual/engine/index"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "doc",
              "id": "manual/engine/custom-runner"
            },
            {
              "type": "doc",
              "id": "manual/engine/custom-registry"
            },
            {
              "type": "doc",
              "id": "manual/engine/custom-ca"
            },
            {
              "type": "doc",
              "id": "manual/engine/proxy"
            },
          ],
        },
        {
          "type": "doc",
          "id": "manual/troubleshooting"
        },
      ]
    },
    /*
    {
      "type": "category",
      "label": "User Manual",
      "link": {
        "type": "doc",
        "id": "manuals/user/index"
      },
      "collapsible": true,
      "collapsed": true,
      "items": [
        {
          "type": "category",
          "label": "Functions and Chaining",
          "link": {
            "type": "doc",
            "id": "manuals/user/functions/functions"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "doc",
              "label": "Function Calls",
              "id": "manuals/user/functions/call"
            },
            {
              "type": "doc",
              "label": "Arguments",
              "id": "manuals/user/functions/arguments"
            },
            {
              "type": "doc",
              "label": "Chaining",
              "id": "manuals/user/functions/chaining"
            }
          ]
        },
        {
          "type": "category",
          "label": "Just-in-Time Artifacts",
          "collapsible": true,
          "collapsed": true,
          "link": {
            "type": "doc",
            "id": "manuals/user/artifacts/artifacts"
          },
          "items": [
            {
              "type": "category",
              "label": "Produce and Inspect Artifacts",
              "link": {
                "type": "doc",
                "id": "manuals/user/artifacts/production/produce"
              },
              "collapsible": true,
              "collapsed": true,
              "items": [
                {
                  "type": "doc",
                  "label": "Just-in-Time Containers",
                  "id": "manuals/user/artifacts/production/containers"
                },
                {
                  "type": "doc",
                  "label": "Just-in-Time Directories",
                  "id": "manuals/user/artifacts/production/directories"
                },
                {
                  "type": "doc",
                  "label": "Just-in-Time Files",
                  "id": "manuals/user/artifacts/production/files"
                },
                {
                  "type": "doc",
                  "label": "Artifact Inspection",
                  "id": "manuals/user/artifacts/production/inspect"
                },
              ]
            },
            {
              "type": "category",
              "label": "Consume Artifacts",
              "link": {
                "type": "doc",
                "id": "manuals/user/artifacts/consumption/consume"
              },
              "collapsible": true,
              "collapsed": true,
              "items": [
                {
                  "type": "doc",
                  "label": "Artifact Export",
                  "id": "manuals/user/artifacts/consumption/export"
                },
                {
                  "type": "doc",
                  "label": "Container Interaction",
                  "id": "manuals/user/artifacts/consumption/terminal"
                },
                {
                  "type": "doc",
                  "label": "Container Publication",
                  "id": "manuals/user/artifacts/consumption/publish"
                },
                {
                  "type": "doc",
                  "label": "Container Command Execution",
                  "id": "manuals/user/artifacts/consumption/exec"
                },
                {
                  "type": "doc",
                  "label": "Containers as Services",
                  "id": "manuals/user/artifacts/consumption/services"
                },
              ]
            },
          ]
        },
        {
          "type": "category",
          "label": "Host Access",
          "link": {
            "type": "doc",
            "id": "manuals/user/host/host"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "doc",
              "label": "Host Filesystem Access",
              "id": "manuals/user/host/host-fs"
            },
            {
              "type": "doc",
              "label": "Host Environment Access",
              "id": "manuals/user/host/host-env"
            },
            {
              "type": "doc",
              "label": "Host Services Access",
              "id": "manuals/user/host/host-services"
            }
          ]
        },
        {
          "type": "category",
          "label": "Remote Resources",
          "link": {
            "type": "doc",
            "id": "manuals/user/remotes/remotes"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "doc",
              "label": "Remote Repositories",
              "id": "manuals/user/remotes/remote-repositories"
            },
            {
              "type": "doc",
              "label": "Remote Container Images",
              "id": "manuals/user/remotes/remote-images"
            },

          ]
        },
        {
          "type": "category",
          "label": "Visualization",
          "link": {
            "type": "doc",
            "id": "manuals/user/visualization/visualization"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "doc",
              "label": "Dagger TUI",
              "id": "manuals/user/visualization/tui"
            },
            {
              "type": "doc",
              "label": "Dagger Cloud",
              "id": "manuals/user/visualization/cloud-get-started"
            },
          ]
        },
        {
          "type": "doc",
          "id": "manuals/user/troubleshooting",
          "label": "Troubleshooting"
        }
      ]
    },
    {
      "type": "category",
      "label": "Developer Manual",
      "collapsible": true,
      "collapsed": true,
      "link": {
        "type": "doc",
        "id": "manuals/developer/index"
      },
      "items": [
        {
          "type": "doc",
          "id": "manuals/developer/modules-vs-functions"
        },
        {
          "type": "doc",
          "id": "manuals/developer/architecture"
        },
        {
          "type": "doc",
          "id": "manuals/developer/execution-environment"
        },
        {
          "type": "doc",
          "id": "manuals/developer/functions"
        },
        {
          "type": "doc",
          "id": "manuals/developer/ide-integration"
        },
        {
          "type": "doc",
          "id": "manuals/developer/module-structure"
        },
        {
          "type": "doc",
          "id": "manuals/developer/documentation"
        },
        {
          "type": "doc",
          "id": "manuals/developer/secrets"
        },
        {
          "type": "doc",
          "id": "manuals/developer/services"
        },
        {
          "type": "doc",
          "id": "manuals/developer/dependencies"
        },
        {
          "type": "doc",
          "id": "manuals/developer/chaining"
        },
        {
          "type": "doc",
          "id": "manuals/developer/cache-volumes"
        },
        {
          "type": "doc",
          "id": "manuals/developer/custom-types"
        },
        {
          "type": "doc",
          "id": "manuals/developer/enumerations"
        },
        {
          "type": "doc",
          "id": "manuals/developer/entrypoint-function"
        },
        {
          "type": "doc",
          "id": "manuals/developer/state-functions"
        },
        {
          "type": "doc",
          "id": "manuals/developer/runtimes"
        },
        {
          "type": "doc",
          "id": "manuals/developer/interfaces"
        },
        {
          "type": "doc",
          "id": "manuals/developer/error-handling"
        },
        {
          "type": "doc",
          "id": "manuals/developer/debugging"
        },
        {
          "type": "doc",
          "label": "Publish Modules",
          "id": "manuals/developer/publish-modules"
        },
        {
          "type": "link",
          "label": "Go SDK Reference",
          "href": "https://pkg.go.dev/dagger.io/dagger"
        },
        {
          "type": "link",
          "label": "Python SDK Reference",
          "href": "https://dagger-io.readthedocs.org/"
        },
        {
          "type": "doc",
          "label": "Typescript SDK Reference",
          "id": "reference/typescript/modules"
        },
        {
          "type": "link",
          "label": "API Reference",
          "href": "https://docs.dagger.io/api/reference"
        },
        {
          "type": "doc",
          "id": "manuals/developer/known-issues"
        },
      ],
    },
    {
      "type": "category",
      "label": "Administrator Manual",
      "link": {
        "type": "doc",
        "id": "manuals/administrator/index"
      },
      "collapsible": true,
      "collapsed": true,
      "items": [
        {
          "type": "doc",
          "id": "manuals/administrator/ci"
        },
        {
          "type": "category",
          "label": "Dagger Engine",
          "link": {
            "type": "doc",
            "id": "manuals/administrator/engine/index"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "doc",
              "label": "Custom Runner",
              "id": "manuals/administrator/engine/custom-runner"
            },
            {
              "type": "doc",
              "label": "Custom Registry",
              "id": "manuals/administrator/engine/custom-registry"
            },
            {
              "type": "doc",
              "label": "Proxy Configuration",
              "id": "manuals/administrator/engine/proxy"
            },
            {
              "type": "doc",
              "label": "Custom Certificate Authorities",
              "id": "manuals/administrator/engine/custom-ca"
            }
          ]
        },
        {
          "type": "category",
          "label": "Dagger Cloud",
          "link": {
            "type": "doc",
            "id": "manuals/administrator/cloud/index"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "doc",
              "id": "manuals/administrator/cloud/roles-permissions"
            },
            {
              "type": "doc",
              "id": "manuals/administrator/cloud/organizations"
            },
            {
              "type": "doc",
              "id": "manuals/administrator/cloud/caching"
            }
          ]
        }
      ]
    },
    */
    {
      "type": "category",
      "label": "Configuring Dagger",
      "link": {
        "type": "doc",
        "id": "configuring/index"
      },
      "collapsible": true,
      "collapsed": true,
      "items": [
        {
          "type": "doc",
          "id": "configuring/custom-runner"
        },
        {
          "type": "doc",
          "id": "configuring/custom-registry"
        },
        {
          "type": "doc",
          "id": "configuring/custom-ca"
        },
        {
          "type": "doc",
          "id": "configuring/proxy"
        },
        {
          "type": "doc",
          "id": "configuring/cloud"
        },

      ],
    },
    {
      "type": "doc",
      "id": "faq"
    },
    {
      "type": "doc",
      "id": "contributing"
    },
    {
      "type": "link",
      "label": "Changelog",
      "href": "https://github.com/dagger/dagger/blob/main/CHANGELOG.md"
    }
  ]
}
