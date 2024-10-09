
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
        "api/types",
        "api/chaining",
        "api/cache-volumes",
        "api/secrets",
        "api/terminal",
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
            "api/clients-http"
          ]
        },
        {
          "type": "category",
          "label": "Writing Custom Functions",
          "link": {
            "type": "doc",
            "id": "api/custom-functions"
          },
          "collapsible": true,
          "collapsed": true,
          "items": [
            "api/arguments",
            "api/return-values",
            "api/ide-integration",
            "api/services",
            "api/packages",
            "api/error-handling",
            "api/documentation",
            "api/enumerations",
            "api/interfaces",
            "api/custom-types",
            "api/constructors",
            "api/fields",
          ]
        },
        {
          "type": "category",
          "label": "Packaging and Publishing Functions",
          "collapsible": true,
          "collapsed": true,
          "items": [
            "api/structure-packaging",
            "api/module-dependencies",
            "api/remote-repositories",
            "api/publish",
          ]
        },
        {
          "type": "category",
          "label": "API and SDKs Reference",
          "collapsible": true,
          "collapsed": true,
          "items": [
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
        "integrations/argo-workflows",
        "integrations/aws-codebuild",
        "integrations/azure-pipelines",
        "integrations/circleci",
        "integrations/github",
        "integrations/gitlab",
        "integrations/google-cloud-run",
        "integrations/java",
        "integrations/jenkins",
        "integrations/kubernetes",
        "integrations/nerdctl",
        "integrations/openshift",
        "integrations/php",
        "integrations/podman",
        "integrations/tekton",
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
        "configuration/custom-runner",
        "configuration/custom-registry",
        "configuration/custom-ca",
        "configuration/proxy",
        "configuration/cloud",
        "configuration/modules",
      ],
    },
    {
      "type": "doc",
      "label": "Cookbook",
      "id": "cookbook/cookbook"
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
      "id": "troubleshooting"
    },
    {
      "type": "doc",
      "id": "contributing"
    },
    {
      "type": "link",
      "label": "Documentation Archive",
      "href": "https://archive.docs.dagger.io"
    },
    {
      "type": "link",
      "label": "Changelog",
      "href": "https://github.com/dagger/dagger/blob/main/CHANGELOG.md"
    }
  ]
}
