
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
            "api/calling"
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
            "api/functions",
            "api/arguments",
            "api/return-values",
            "api/chaining",
            "api/debugging",
            "api/documentation",
            "api/secrets",
            "api/services",
            "api/cache-volumes",
            "api/host-resources",
            "api/error-handling",
            "api/structure-packaging",
            "api/entrypoint-function",
            "api/custom-types",
            "api/state-functions",
            "api/enumerations",
            "api/interfaces",
            "api/runtimes",
            "api/language-dependencies",
            "api/ide-integration",
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
      "type": "doc",
      "label": "Cookbook",
      "id": "cookbook/cookbook"
    },
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
      "id": "contributing"
    },
    {
      "type": "doc",
      "id": "faq"
    },
    {
      "type": "link",
      "label": "Changelog",
      "href": "https://github.com/dagger/dagger/blob/main/CHANGELOG.md"
    }
  ]
}
