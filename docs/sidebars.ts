
module.exports = {
  "current": [
    {
      "type": "doc",
      "label": "Introduction",
      "id": "index",
    },
    {
      "type": "category",
      "label": "Features",
      "link": {
        "type": "doc",
        "id": "features/index",
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
      "type": "category",
      "label": "Dagger API",
      "link": {
        "type": "doc",
        "id": "api/index"
      },
      "collapsible": true,
      "collapsed": true,
      "items": [
        {
          "type": "category",
          "label": "Calling the API",
          "link": {
            "type": "doc",
            "id": "api/calling"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            "api/clients-sdk",
            "api/clients-cli",
            "api/clients-graphql"
          ]
        },
        {
          "type": "category",
          "label": "Extending the API",
          "link": {
            "type": "doc",
            "id": "api/extending"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            {
              "type": "category",
              "label": "Dagger Functions",
              "link": {
                "type": "doc",
                "id": "api/functions"
              },
              items: [
                "api/arguments",
                "api/return-values",
                "api/chaining",
                "api/secrets",
                "api/services",
                "api/cache-volumes",
                "api/host-resources",
                "api/error-handling",
                "api/debugging",
                "api/ide-integration",
                "api/documentation",
                "api/structure-packaging",
                "api/entrypoint-function",
                "api/custom-types",
                "api/state-functions",
                "api/enumerations",
                "api/interfaces",
                "api/runtimes",
                "api/language-dependencies",
              ]
            },
          ]
        },
        {
          "type": "category",
          "label": "Reference",
          "collapsible": true,
          "collapsed": true,
          "items": [
            "api/types",
            "api/internals",
            {
              "type": "link",
              "label": "API Reference",
              "href": "https://docs.dagger.io/api/reference"
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
              "label": "TypeScript SDK Reference",
              "id": "reference/typescript/modules"
            },

          ]
        },
        {
          "type": "doc",
          "label": "CLI Reference",
          "id": "reference/cli"
        },
      ]
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
      "label": "Configuration",
      "link": {
        "type": "doc",
        "id": "configuration/index"
      },
      "collapsible": true,
      "collapsed": true,
      "items": [
        {
          "type": "doc",
          "id": "configuration/custom-runner"
        },
        {
          "type": "doc",
          "id": "configuration/custom-registry"
        },
        {
          "type": "doc",
          "id": "configuration/custom-ca"
        },
        {
          "type": "doc",
          "id": "configuration/proxy"
        },
        {
          "type": "doc",
          "id": "configuration/cloud"
        },

      ],
    },
    {
      "type": "doc",
      "label": "Cookbook",
      "id": "cookbook/cookbook"
    },
    {
      "type": "doc",
      "id": "faq"
    },
    {
      "type": "doc",
      "label": "Adopting Dagger",
      "id": "adopting"
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
