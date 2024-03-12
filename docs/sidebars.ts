
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
        "quickstart/conclusion"
      ]
    },
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
          "label": "Dagger in CI",
          "link": {
            "type": "doc",
            "id": "manuals/user/ci/index"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "doc",
              "id": "manuals/user/ci/github-actions"
            },
            {
              "type": "doc",
              "id": "manuals/user/ci/gitlab"
            },
            {
              "type": "doc",
              "id": "manuals/user/ci/circleci"
            },
            {
              "type": "doc",
              "id": "manuals/user/ci/jenkins"
            },
            {
              "type": "doc",
              "id": "manuals/user/ci/aws-codebuild"
            },
            {
              "type": "doc",
              "id": "manuals/user/ci/azure-pipelines"
            },
            {
              "type": "doc",
              "id": "manuals/user/ci/argo-workflows"
            }
          ]
        },
        {
          "type": "category",
          "label": "Dagger Cloud",
          "link": {
            "type": "doc",
            "id": "manuals/user/cloud/index"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "doc",
              "label": "Get Started",
              "id": "manuals/user/cloud/get-started"
            },
            {
              "type": "doc",
              "id": "manuals/user/cloud/user-interface"
            },
            {
              "type": "doc",
              "id": "manuals/user/cloud/roles-permissions"
            },
            {
              "type": "doc",
              "id": "manuals/user/cloud/org-administration"
            }
          ]
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
          "type": "category",
          "label": "Overview",
          "link": {
            "type": "doc",
            "id": "manuals/developer/overview/index"
          },
          "items": [
            {
              "type": "doc",
              "id": "manuals/developer/overview/modules-vs-functions"
            },
            {
              "type": "doc",
              "id": "manuals/developer/overview/architecture"
            },
            {
              "type": "doc",
              "id": "manuals/developer/overview/execution-environment"
            },
            {
              "type": "doc",
              "id": "manuals/developer/overview/dependencies"
            }
          ]
        },
        {
          "type": "category",
          "label": "Developing with Go",
          "link": {
            "type": "doc",
            "id": "manuals/developer/go/index"
          },
          "items": [
            {
              "type": "doc",
              "id": "manuals/developer/go/first-module"
            },
            {
              "type": "doc",
              "id": "manuals/developer/go/functions"
            },
            {
              "type": "doc",
              "id": "manuals/developer/go/documentation"
            },
            {
              "type": "doc",
              "id": "manuals/developer/go/secrets"
            },
            {
              "type": "doc",
              "id": "manuals/developer/go/dependencies"
            },
            {
              "type": "doc",
              "id": "manuals/developer/go/chaining"
            },
            {
              "type": "doc",
              "id": "manuals/developer/go/constructor"
            },
            {
              "type": "doc",
              "id": "manuals/developer/go/interfaces"
            },
            {
              "type": "doc",
              "id": "manuals/developer/go/visibility"
            },
            {
              "type": "doc",
              "id": "manuals/developer/go/custom-types"
            },
            {
              "type": "doc",
              "id": "manuals/developer/go/error-handling"
            },
            {
              "type": "doc",
              "id": "manuals/developer/go/module-structure"
            },
            {
              "type": "doc",
              "id": "manuals/developer/go/ide-integration"
            },
            {
              "type": "doc",
              "id": "manuals/developer/go/debugging"
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
            "id": "manuals/developer/python/index"
          },
          "items": [
            {
              "type": "doc",
              "id": "manuals/developer/python/first-module"
            },
            {
              "type": "doc",
              "id": "manuals/developer/python/functions"
            },
            {
              "type": "doc",
              "id": "manuals/developer/python/documentation"
            },
            {
              "type": "doc",
              "id": "manuals/developer/python/secrets"
            },
            {
              "type": "doc",
              "id": "manuals/developer/python/dependencies"
            },
            {
              "type": "doc",
              "id": "manuals/developer/python/chaining"
            },
            {
              "type": "doc",
              "id": "manuals/developer/python/constructor"
            },
            {
              "type": "doc",
              "id": "manuals/developer/python/attribute-functions"
            },
              {
              "type": "doc",
              "id": "manuals/developer/python/custom-types"
            },
            {
              "type": "doc",
              "id": "manuals/developer/python/name-overrides"
            },
            {
              "type": "doc",
              "id": "manuals/developer/python/error-handling"
            },
            {
              "type": "doc",
              "id": "manuals/developer/python/module-structure"
            },
            {
              "type": "doc",
              "id": "manuals/developer/python/python-dependencies"
            },
            {
              "type": "doc",
              "id": "manuals/developer/python/ide-integration"
            },
            {
              "type": "doc",
              "id": "manuals/developer/python/debugging"
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
            "id": "manuals/developer/typescript/index"
          },
          "items": [
            {
              "type": "doc",
              "id": "manuals/developer/typescript/first-module"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/functions"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/documentation"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/secrets"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/dependencies"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/chaining"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/constructor"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/custom-types"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/decorators"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/aliases"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/visibility"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/error-handling"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/module-structure"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/typescript-dependencies"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/ide-integration"
            },
            {
              "type": "doc",
              "id": "manuals/developer/typescript/debugging"
            },
            {
              "type": "doc",
              "label": "Reference",
              "id": "reference/typescript/modules"
            }
          ]
        },
        {
          "type": "link",
          "label": "API Reference",
          "href": "https://docs.dagger.io/api/reference"
        },
        {
          "type": "doc",
          "id": "manuals/developer/known-issues"
        }
      ]
    },
    {
      "type": "doc",
      "label": "CLI Reference",
      "id": "reference/cli"
    },
    {
      "type": "doc",
      "id": "tutorials",
      "label": "Tutorials"
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
