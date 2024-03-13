
module.exports = {
  "current": [
    {
      "type": "doc",
      "id": "index",
      "label": "Introduction"
    },
    {
      "type": "doc",
      "label": "Installation",
      "id": "install"
    },
    {
      "type": "category",
      "label": "Quickstart",
      "items": [
        "quickstart/index",
        "quickstart/cli",
        "quickstart/hello",
        "quickstart/arguments",
        "quickstart/directories",
        "quickstart/containers",
        "quickstart/daggerize",
        "quickstart/custom-function",
        "quickstart/daggerverse",
        "quickstart/conclusion"
      ]
    },
    {
      "type": "category",
      "label": "User Guide",
      "link": {
        "type": "doc",
        "id": "user-guide/index"
      },
      "collapsible": true,
      "collapsed": true,
      "items": [
        {
          "type": "category",
          "label": "Dagger Cloud",
          "link": {
            "type": "doc",
            "id": "user-guide/cloud/index"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "doc",
              "label": "Get Started",
              "id": "user-guide/cloud/get-started"
            },
            {
              "type": "doc",
              "id": "user-guide/cloud/user-interface"
            }
          ]
        },
        {
          "type": "doc",
          "id": "user-guide/troubleshooting",
          "label": "Troubleshooting"
        }
      ]
    },
    {
      "type": "category",
      "label": "Developer Guide",
      "collapsible": true,
      "collapsed": true,
      "link": {
        "type": "doc",
        "id": "developer-guide/index"
      },
      "items": [
        {
          "type": "category",
          "label": "Overview",
          "link": {
            "type": "doc",
            "id": "developer-guide/overview/index"
          },
          "items": [
            {
              "type": "doc",
              "id": "developer-guide/overview/modules-vs-functions"
            },
            {
              "type": "doc",
              "id": "developer-guide/overview/architecture"
            },
            {
              "type": "doc",
              "id": "developer-guide/overview/execution-environment"
            },
            {
              "type": "doc",
              "id": "developer-guide/overview/dependencies"
            }
          ]
        },
        {
          "type": "category",
          "label": "Developing with Go",
          "link": {
            "type": "doc",
            "id": "developer-guide/go/index"
          },
          "items": [
            {
              "type": "doc",
              "id": "developer-guide/go/first-module"
            },
            {
              "type": "doc",
              "id": "developer-guide/go/functions"
            },
            {
              "type": "doc",
              "id": "developer-guide/go/documentation"
            },
            {
              "type": "doc",
              "id": "developer-guide/go/secrets"
            },
            {
              "type": "doc",
              "id": "developer-guide/go/dependencies"
            },
            {
              "type": "doc",
              "id": "developer-guide/go/chaining"
            },
            {
              "type": "doc",
              "id": "developer-guide/go/constructor"
            },
            {
              "type": "doc",
              "id": "developer-guide/go/interfaces"
            },
            {
              "type": "doc",
              "id": "developer-guide/go/visibility"
            },
            {
              "type": "doc",
              "id": "developer-guide/go/custom-types"
            },
            {
              "type": "doc",
              "id": "developer-guide/go/error-handling"
            },
            {
              "type": "doc",
              "id": "developer-guide/go/module-structure"
            },
            {
              "type": "doc",
              "id": "developer-guide/go/ide-integration"
            },
            {
              "type": "doc",
              "id": "developer-guide/go/debugging"
            },
            {
              "type": "link",
              "label": "Go SDK Reference",
              "href": "https://pkg.go.dev/dagger.io/dagger"
            }
          ]
        },
        {
          "type": "category",
          "label": "Developing with Python",
          "link": {
            "type": "doc",
            "id": "developer-guide/python/index"
          },
          "items": [
            {
              "type": "doc",
              "id": "developer-guide/python/first-module"
            },
            {
              "type": "doc",
              "id": "developer-guide/python/functions"
            },
            {
              "type": "doc",
              "id": "developer-guide/python/documentation"
            },
            {
              "type": "doc",
              "id": "developer-guide/python/secrets"
            },
            {
              "type": "doc",
              "id": "developer-guide/python/dependencies"
            },
            {
              "type": "doc",
              "id": "developer-guide/python/chaining"
            },
            {
              "type": "doc",
              "id": "developer-guide/python/constructor"
            },
            {
              "type": "doc",
              "id": "developer-guide/python/attribute-functions"
            },
              {
              "type": "doc",
              "id": "developer-guide/python/custom-types"
            },
            {
              "type": "doc",
              "id": "developer-guide/python/name-overrides"
            },
            {
              "type": "doc",
              "id": "developer-guide/python/error-handling"
            },
            {
              "type": "doc",
              "id": "developer-guide/python/module-structure"
            },
            {
              "type": "doc",
              "id": "developer-guide/python/python-dependencies"
            },
            {
              "type": "doc",
              "id": "developer-guide/python/ide-integration"
            },
            {
              "type": "doc",
              "id": "developer-guide/python/debugging"
            },
            {
              "type": "link",
              "label": "Python SDK Reference",
              "href": "https://dagger-io.readthedocs.org/"
            }
          ]
        },
        {
          "type": "category",
          "label": "Developing with TypeScript",
          "link": {
            "type": "doc",
            "id": "developer-guide/typescript/index"
          },
          "items": [
            {
              "type": "doc",
              "id": "developer-guide/typescript/first-module"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/functions"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/documentation"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/secrets"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/dependencies"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/chaining"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/constructor"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/custom-types"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/decorators"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/aliases"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/visibility"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/error-handling"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/module-structure"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/typescript-dependencies"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/ide-integration"
            },
            {
              "type": "doc",
              "id": "developer-guide/typescript/debugging"
            },
            {
              "type": "doc",
              "label": "Reference",
              "id": "reference/typescript/modules"
            }
          ]
        },
        {
          "type": "doc",
          "label": "Publish Modules",
          "id": "guides/publish-modules"
        },
        {
          "type": "doc",
          "id": "guides",
          "label": "Guides"
        },
        {
          "type": "link",
          "label": "API Reference",
          "href": "https://docs.dagger.io/api/reference"
        },
        {
          "type": "doc",
          "id": "developer-guide/known-issues"
        }
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
          "type": "category",
          "label": "Integrations",
          "link": {
            "type": "doc",
            "id": "manuals/administrator/integrations/index"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "doc",
              "id": "manuals/administrator/integrations/argo-workflows"
            },
            {
              "type": "doc",
              "id": "manuals/administrator/integrations/aws-codebuild"
            },
            {
              "type": "doc",
              "id": "manuals/administrator/integrations/azure-pipelines"
            },
            {
              "type": "doc",
              "id": "manuals/administrator/integrations/circleci"
            },
            {
              "type": "doc",
              "id": "manuals/administrator/integrations/github-actions"
            },
            {
              "type": "doc",
              "id": "manuals/administrator/integrations/gitlab"
            },
            {
              "type": "doc",
              "id": "manuals/administrator/integrations/jenkins"
            },
            {
              "type": "doc",
              "id": "manuals/administrator/integrations/openshift"
            },
            {
              "type": "doc",
              "id": "manuals/administrator/integrations/kubernetes"
            },
            {
              "type": "doc",
              "id": "manuals/administrator/integrations/tekton"
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
            }
          ]
        },
        {
          "type": "doc",
          "label": "Tutorials",
          "id": "manuals/administrator/tutorials"
        },
      ]
    },
    {
      "type": "doc",
      "label": "CLI Reference",
      "id": "reference/cli"
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
