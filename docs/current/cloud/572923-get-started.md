---
slug: /cloud/572923/get-started
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Get Started with Dagger Cloud

## Introduction

This guide helps you get started with Dagger Cloud.

Here are the steps you’ll follow in this guide:

- Sign up for Dagger Cloud
- Connect Dagger Cloud with your CI provider or CI tool
- Visualize CI runs with Dagger Cloud
- Improve CI performance with Dagger Cloud's distributed caching

The next sections will walk you through these steps in detail.

## Prerequisites

This guide assumes that:

- You have an understanding of how Dagger works. If not, [read the Dagger Quickstart](../quickstart/index.mdx).
- You have a GitHub account (required for Dagger Cloud identity verification). If not, [register for a free GitHub account](https://github.com/signup).
- You have a source code repository and a Dagger pipeline that interacts with it. If not, follow the steps in Appendix A to [create and populate a GitHub repository with a sample application and Dagger pipeline](#appendix-a-create-a-dagger-pipeline).

## Step 1: Sign up for Dagger Cloud

:::info
At the end of this step, you will have signed up for Dagger Cloud and obtained a Dagger Cloud token. If you already have a Dagger Cloud account and token, you may skip this step.
:::

Follow the steps below to sign up for Dagger Cloud, create an organization and obtain a Dagger Cloud token.

1. Browse to the Dagger Cloud website at https://www.dagger.com/cloud/product.
1. Click "Continue with GitHub" to log in with your GitHub account.

  [image]

1. On the GitHub authorization screen, confirm the Dagger Cloud connection for authentication.Once authorized, you will be redirected to a welcome page and prompted to create a new organization. Enter a name for your organization in the "Organization Name" field. Click "Next" to proceed.

  :::info
  The organization name can only contain alphanumeric characters and dashes and is unique across Dagger Cloud.
  :::

  [image]

1. Review the available Dagger Cloud subscription plans. Choose a plan by clicking "Select".
1. If you selected a team plan, you will be presented with the option to add teammates to your Dagger Cloud account. Enter one or more email addresses as required. Click"Next" to proceed.

  :::note
  This step is optional and not available in individual plans.
  :::

  [image]

1. Enter the required payment details. Click "Set payment method" to proceed.

  [image]

1. Your payment information will now be verified. If all is well, your new organization will be created and you will be redirected to a success page confirming that your Dagger Cloud account and organization have been created.

  [image]

1. Click "Go to dashboard" to visit the Dagger Cloud dashboard, which allows you to manage your Dagger Cloud organization and account.

  [image]

1. Click "Connect Dagger Cloud". You will be redirected to a page containing your Dagger Cloud token. Note this token carefully, as you will need it to connect your Dagger Cloud account with your CI provider.

  [image]

  :::tip
  You can view this token at any time by browsing to the *Organization Settings* -> *Organization* page of the Dagger Cloud dashboard (accessible by clicking your user profile icon in the Dagger Cloud interface), selecting your organization and navigating to the *Configuration* tab.
  :::

  [image]

## Step 2: Connect Dagger Cloud with your CI

:::info
At the end of this step, you will have connected Dagger Cloud with your CI provider or CI tool using your Dagger Cloud token. If you have already connected Dagger Cloud with your CI provider/tool, you may skip this step.
:::

The Dagger Cloud dashboard will not display any data until you connect your Dagger Cloud account with a CI provider or CI tool. The general procedure to do this is:

- Store the token as a secret with your CI provider/in your CI tool.
- Add the secret to your CI environment as a variable named DAGGER_CLOUD_TOKEN.

:::danger
You must store this token as a secret (not plaintext) with your CI provider and reference it in your CI’s workflow. Using a secret is recommended to protect your Dagger Cloud account from being used by forks of your project.
:::

<Tabs groupId="ci">
<TabItem value="GitHub Actions">

1. Create a new secret for your GitHub repository named `DAGGER_TOKEN`, and set it to the value of the token obtained in [Step 1](#step-1-sign-up-for-dagger-cloud). Refer to the GitHub documentation [on creating repository secrets](https://docs.github.com/en/actions/security-guides/using-secrets-in-github-actions#creating-secrets-for-a-repository).

1. Update your GitHub Actions workflow and add the secret to your `dagger run` step as an environment variable. The environment variable must be named `DAGGER_CLOUD_TOKEN` and can be referenced in the workflow using the format `DAGGER_CLOUD_TOKEN: ${{ secrets.DAGGER_TOKEN }}`. Refer to the GitHub documentation on [using secrets in a workflow](https://docs.github.com/en/actions/security-guides/using-secrets-in-github-actions#using-secrets-in-a-workflow).

1. Install the [Dagger Cloud GitHub App](https://github.com/apps/dagger-cloud). Once installed, GitHub automatically adds a new check for your GitHub pull requests, with a link to see CI status for each workflow run in Dagger Cloud.

  [image]

Here is a sample GitHub Actions workflow file with the Dagger Cloud integration highlighted:

```yaml title=".github/workflows/dagger.yml" file=./snippets/get-started/go/actions.yml
```

:::tip
You can use this file with the starter application and Dagger pipeline in [Appendix A](#appendix-a-create-a-dagger-pipeline) to test your Dagger Cloud/GitHub Actions integration.
:::

</TabItem>
<TabItem value="GitLab CI">

1. Create a new CI/CD project variable in your GitLab project named `DAGGER_TOKEN`, and set it to the value of the token obtained in [Step 1](#step-1-sign-up-for-dagger-cloud). Ensure that you configure the project variable to be masked and protected. Refer to the GitLab documentation on [creating CI/CD project variables](https://docs.gitlab.com/ee/ci/variables/#define-a-cicd-variable-in-the-ui) and [CI/CD variable security](https://docs.gitlab.com/ee/ci/variables/#cicd-variable-security).

1. Update your GitLab CI workflow and add the variable to your CI environment. The environment variable must be named `DAGGER_CLOUD_TOKEN`. Refer to the GitLab documentation on [using CI/CD variables](https://docs.gitlab.com/ee/ci/variables/index.html#use-cicd-variables-in-job-scripts).

1. GitLab makes some metadata available only to merge request pipelines. If your CI workflow relies entirely on merge requests, you can improve your Dagger Cloud experience by optionally adding a workflow rule to create a pipeline only on merge requests. Refer to the GitLab documentation on [merge requests](https://docs.gitlab.com/ee/user/project/merge_requests/) and [workflow rules](https://docs.gitlab.com/ee/ci/yaml/workflow.html#switch-between-branch-pipelines-and-merge-request-pipelines).

  :::danger
  This step is optional and should be performed only after an in-depth analysis of the impact on your CI workflow.
  :::

Here is a sample GitLab CI workflow with the Dagger Cloud integration highlighted:

```yaml title=".gitlab-ci.yml" file=./snippets/get-started/go/gitlab.yml
```

</TabItem>
<TabItem value="CircleCI">

1. Create a new environment variable in your CircleCI project named `DAGGER_CLOUD_TOKEN` and set it to the value of the token obtained in [Step 1](#step-1-sign-up-for-dagger-cloud). Refer to the CircleCI documentation on [creating environment variables for a project](https://circleci.com/docs/set-environment-variable/#set-an-environment-variable-in-a-project).

1. If your source code is stored in a GitHub, GitLab or Atlassian Bitbucket repository, update your CircleCI workflow and add the following pipeline values to the CI environment. Refer to the CircleCI documentation on [using pipeline values](https://circleci.com/docs/variables/#pipeline-values).

  GitHub:

  ```yaml
  environment:
    CIRCLE_PIPELINE_NUMBER: << pipeline.number
  ```

  GitLab:

  ```yaml
  environment:
    CIRCLE_PIPELINE_NUMBER: << pipeline.number >>
    CIRCLE_PIPELINE_TRIGGER_LOGIN: << pipeline.trigger_parameters.gitlab.user_username >>
    CIRCLE_PIPELINE_REPO_URL: << pipeline.trigger_parameters.gitlab.repo_url >>
    CIRCLE_PIPELINE_REPO_FULL_NAME: << pipeline.trigger_parameters.gitlab.repo_name >>
  ```

  Atlassian BitBucket:

  ```yaml
  environment:
    CIRCLE_PIPELINE_NUMBER: << pipeline.number
  ```

Here is a sample CircleCI workflow. The `DAGGER_CLOUD_TOKEN` environment variable will be automatically injected.

```yaml title=".circleci/config.yml" file=./snippets/get-started/go/circle.yml
```

</TabItem>
<TabItem value="Jenkins">

1. Configure a Jenkins credential named `DAGGER_TOKEN` and set it to the value of the token obtained in [Step 1](#step-1-sign-up-for-dagger-cloud). Refer to the Jenkins documentation on [creating credentials](https://www.jenkins.io/doc/book/using/using-credentials/#configuring-credentials) and [credential security](https://www.jenkins.io/doc/book/using/using-credentials/#credential-security).

1. Update your Jenkins Pipeline and add the variable to the CI environment. The environment variable must be named `DAGGER_CLOUD_TOKEN` and can be referenced in the Pipeline environment using the format `DAGGER_CLOUD_TOKEN = credentials('DAGGER_TOKEN')`. Refer to the Jenkins documentation on [handling credentials](https://www.jenkins.io/doc/book/pipeline/jenkinsfile/#handling-credentials).

  :::note
  If you use the same Jenkins server for more than one Dagger Cloud organization, create distinct credentials for each organization and link them to their respective Dagger Cloud tokens.
  :::

Here is a sample Jenkins Pipeline with the Dagger Cloud integration highlighted:

```groovy title="Jenkinsfile" file=./snippets/get-started/go/Jenkinsfile
```

</TabItem>
<TabItem value="ArgoCD">

1. Create a Kubernetes secret and set it to the value of the token obtained in [Step 1](#step-1-sign-up-for-dagger-cloud) with the following command (replace the `TOKEN` placeholder with the token value). Refer to the Kubernetes documentation on [creating secrets](https://kubernetes.io/docs/concepts/configuration/secret/) for more information.

  ```shell
  kubectl create secret generic dagger-cloud-token --from-literal=token={TOKEN}
  ```

1. Update your ArgoCD Workflow and add the secret to the CI environment. Refer to the ArgoCD documentation on [using secrets](https://argoproj.github.io/argo-workflows/walk-through/secrets/).

Here is a sample ArgoCD Workflow with the Dagger Cloud integration highlighted:

```yaml file=./snippets/get-started/go/argocd-workflow.yml
```

:::note
This ArgoCD Workflow is significantly abbreviated for this guide.
:::

</TabItem>
</Tabs>

## Step 3: Visualize CI runs with Dagger Cloud

Once your CI provider/tool is connected with Dagger Cloud, it’s time to test the integration.

To do this, trigger your CI workflow and Dagger pipeline by pushing a commit or opening a pull request. If you are using the starter application and pipeline from Appendix A, use the following commands:

```shell
sed -i 's/Welcome to Dagger/Welcome to Dagger Cloud/g' src/App.tsx
git commit -a -m “Updated welcome message”
git push
```

Once your CI workflow begins, navigate to the *Runs* page of the Dagger Cloud dashboard. You should see your most recent CI run as the first entry in the table, as shown below:

[image]

A run represents one invocation of a Dagger pipeline. It contains detailed information about the steps performed by the pipeline.

The *Runs* page provides an overview of all runs. You can drill down into the details of a specific run by clicking it. This directs you to a run-specific Run Details page, as shown below:

[image]

The *Run Details* page includes detailed status and duration metadata about the pipeline steps. You can opt to view the details using either the tree view or the Directed Acyclic Graph (DAG) view.

- The tree view shows Dagger pipelines and steps within those pipelines. If there are any errors in the run, Dagger Cloud automatically brings you to the first error in the list. You see detailed logs and output of each step, making it easy for you to debug your pipelines and collaborate with your teammates.

  [image]

- The DAG view displays a graph of everything that happened in a Dagger run and shows the status for each step. Click on a node in the graph to see detailed logs for a step.

  [image]

## Step 4: Improve CI performance with Dagger Cloud’s distributed caching

Dagger already comes with built-in support for [cache volumes](../quickstart/635927-caching.mdx), which can be used to cache packages and thereby avoid unnecessary rebuilds and test reruns. One of Dagger Cloud's most powerful features is its distributed cache, which enhances this support significantly and allows multiple machines, including ephemeral runners, to intelligently share a distributed cache.

Dagger Cloud automatically detects and creates cache volumes when they are declared in your code. To see how this works, add a cache volume to your Dagger pipeline and then trigger a CI run. If you're using the starter application and Dagger pipeline from [Appendix A](#appendix-a-create-a-dagger-pipeline), do this by updating the Dagger pipeline code as shown below:

```go
package main

import (
  "context"
  "fmt"
  "math"
  "math/rand"
  "os"

  "dagger.io/dagger"
)

func main() {
  ctx := context.Background()

  // initialize Dagger client
  client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))

  if err != nil {
    panic(err)
  }
  defer client.Close()

  nodeCache := client.CacheVolume("node")

  source := client.Container().
    From("node:16").
    WithDirectory("/src", client.Host().Directory("."), dagger.ContainerWithDirectoryOpts{
      Exclude: []string{"node_modules/", "ci/"},
    }).
    WithMountedCache("/src/node_modules", nodeCache)

  runner := source.WithWorkdir("/src").
    WithExec([]string{"npm", "install"})

  test := runner.WithExec([]string{"npm", "test", "--", "--watchAll=false"})

  _, err = test.WithExec([]string{"npm", "run", "build"}).
    Directory("./build").
    Export(ctx, "./build")

  if err != nil {
    panic(err)
  }

  ref, err := client.Container().
    From("nginx:1.23-alpine").
    WithDirectory("/usr/share/nginx/html", client.Host().Directory("./build")).
    Publish(ctx, fmt.Sprintf("ttl.sh/hello-dagger-%.0f", math.Floor(rand.Float64()*10000000))) //#nosec
  if err != nil {
    panic(err)
  }

  fmt.Printf("Published image to: %s\n", ref)
}
```

This revised pipeline now uses a cache volume for the application dependencies.

- It uses the client's `CacheVolume()` method to initialize a new cache volume.
- It uses the `Container.WithMountedCache() method to mount this cache volume at the node_modules/ mount point in the container.

Next, trigger your CI workflow by pushing a commit or opening a pull request. Once your CI workflow begins, browse to the *Organization Settings* -> *Organization* page of the Dagger Cloud dashboard (accessible by clicking your user profile icon in the Dagger Cloud interface) and navigate to the *Configuration* tab. You should see the newly-created cache volume listed and enabled.

[image]

You can create as many cache volumes as needed and manage them from the *Configuration* tab of your Dagger Cloud organization page.

[image]

## Conclusion

This guide introduced you to Dagger Cloud and walked you registering a new organization, integrating Dagger Cloud with your CI provider/tool, and using Dagger Cloud’s visualization and caching features. For more information and technical support, visit the [Dagger Cloud reference pages](./reference/index.md) or open a support ticket.

## Appendix A: Create a Dagger pipeline

Before you can integrate Dagger Cloud into your CI process, you need a Dagger pipeline and source code for it to interact with.

If you don’t have these already, follow the steps below to create an application and its accompanying Dagger pipeline.

:::note
This guide uses the starter React application and Dagger pipeline from the [Dagger Quickstart](../quickstart/index.mdx) in tandem with a GitHub repository. If you wish to use a different application or a different VCS, adapt the steps below accordingly.
:::

1. Begin by cloning the example application's repository:

  ```shell
  git clone https://github.com/dagger/hello-dagger.git
  ```

1. In the application directory, create a new Go module:

  ```shell
  cd hello-dagger
  go mod init main
  go get dagger.io/dagger
  ```

1. Create a file named `main.go` and add the following code to it.

  ```go
  package main

  import (
    "context"
    "fmt"
    "math"
    "math/rand"
    "os"

    "dagger.io/dagger"
  )

  func main() {
    ctx := context.Background()

    client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))

    if err != nil {
      panic(err)
    }
    defer client.Close()

    source := client.Container().
      From("node:16").
      WithDirectory("/src", client.Host().Directory("."), dagger.ContainerWithDirectoryOpts{
        Exclude: []string{"node_modules/", "ci/"},
      })

    runner := source.WithWorkdir("/src").
      WithExec([]string{"npm", "install"})

    test := runner.WithExec([]string{"npm", "test", "--", "--watchAll=false"})

    _, err = test.WithExec([]string{"npm", "run", "build"}).
      Directory("./build").
      Export(ctx, "./build")

    if err != nil {
      panic(err)
    }

    ref, err := client.Container().
      From("nginx:1.23-alpine").
      WithDirectory("/usr/share/nginx/html", client.Host().Directory("./build")).
      Publish(ctx, fmt.Sprintf("ttl.sh/hello-dagger-%.0f", math.Floor(rand.Float64()*10000000))) //#nosec
    if err != nil {
      panic(err)
    }

    fmt.Printf("Published image to: %s\n", ref)
  }
  ```

  This Dagger pipeline uses the Dagger Go SDK to test, build and publish a containerized version of the application to a public registry.

  :::info
  Explaining the details of how this pipeline works is outside the scope of this guide; however, you can find a detailed explanation in the [Dagger Quickstart](../quickstart/730264-publish.mdx).
  :::

  [TODO: Add pipeline code in other SDKs]

1. Commit the changes:

  ```shell
  git add .
  git commit -a -m "Added Dagger pipeline"
  ```

1. Create a private repository in your GitHub account and push the changes to it:

  ```shell
  git remote remove origin
  gh auth login
  gh repo create hello-dagger --push --source . --private
  ```
