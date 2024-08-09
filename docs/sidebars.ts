
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
          "id": "integrations/containerd"
        },
        {
          "type": "doc",
          "id": "integrations/circleci"
        },
        {
          "type": "doc",
          "id": "integrations/github-actions"
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
      "type": "doc",
      "label": "Cookbook",
      "id": "cookbook/cookbook"
    },
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
