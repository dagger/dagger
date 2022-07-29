---
slug: /1243/dagger-cloud
displayed_sidebar: '0.2'
---

# Debugging with Dagger Cloud

<p><a href="https://dagger.cloud/" target="_blank" rel="external"> Dagger Cloud </a> is under development, but we have just released the first telemetry feature!</p>

:::tip
Ensure you are using `dagger` CLI version `0.2.18` or higher for Dagger Cloud.
:::

To take advantage of this feature, you will need to create an account by following these steps:

1. Initiate a login process using the CLI with `dagger login`.
2. A new window will open in the browser requesting to sign-up using GitHub.
3. After authenticating, authorize the Dagger GitHub application to finish the process.
4. The CLI is now authenticated, it's OK to close the browser window.

Once you complete the above, your `dagger do` executions will appear under your <a href="https://dagger.cloud/" target="_blank" rel="external"> Dagger Cloud </a> account, and look similar to:

![Dagger Cloud run URL](/img/dagger-cloud/runs.png)

When you click on a specific run, you can see the following:

- Overview - An overview of the `dagger do` execution results. The presented information will highlight the status of the execution (succeed, failed), contextual information about the environment, and a detailed view of each action with their corresponding log outputs.
- Shareable Run URL - A unique URL only accessible by the owner of the execution as well as some specialized Dagger engineers.
- CUE Plans - The raw execution plan. This provides understanding about how Dagger resolved the action dependencies as well as the CUE evaluation results.
- Actions - All the events involved in the action execution with their corresponding duration and outputs.
- CLI Argument view - Arguments specified in the CLI when running `dagger do <action> [flags]`.

With this information, we’ve made it easier for you to inspect your runs with a more verbose failure output.

If you are still struggling with your run, we have provided an easy way for you to request help from our team. You can follow the instructions below to submit your request.

## How to Submit a Help Request

Once you have created an account, you can easily create help requests that will be pre-filled with all of your run information. By providing this information, our team can help debug your issue much faster. Follow the steps below to submit your request:

1. Click on the “ask for help” button in your single run view
2. Once you click on “ask for help”, you will be redirected to a GitHub issue that is pre-filled with all of the information that our team needs to review your request.
3. Click “submit” to publish your issue
4. Now, you will be able to see your issue on the “help” tab of your account or in GitHub directly.

Once we have your request, our team will assign a team member to review and help with your request. They will get back to you directly through the GitHub issue.

## FAQ

### Who can access my execution URL?

Only you & the Dagger team can access the information in the execution URL.

### What information gets sent to Dagger Cloud?

Dagger Cloud only collects **run specific metadata** from your Dagger executions. This currently includes your main plan definition **without** imported packages, execution logs and
non-secret outputs. This informaton is only accessible by the Dagger user who triggered the execution and can't be shared with anyone outside of the Dagger support team.

We don't collect or send any sensitive information like source code, secrets, env variables or private packages to our servers.

### What if I don’t want to open a public issue?

A public issue is the best way for us to communicate since it helps us track the completion of your request, but you can always reach out to us on Discord directly if you don’t want to open an issue. When you reach out to us, please share your Dagger run URL, so we can troubleshoot the issue as quickly as possible.

You can find the shareable Dagger run URL by clicking on the copy icon where it says “share my run”. The contents of this link are only accessible from our Dagger team and yourself. See example below:

<br></br>

![Dagger Cloud run URL](/img/dagger-cloud/share-url.png)

### How do I stop sending telemetry to Dagger Cloud?

By running `dagger logout` the CLI is disconnected from Dagger Cloud.
This will make the CLI behave as it did before this feature was introduced.
Since Dagger Cloud is still under heavy development - accurate as of July 2022 - this is a quick way of solving unexpected integration issues.
