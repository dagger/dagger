---
slug: /1241/dagger-cloud
displayed_sidebar: "0.2"
---

# Dagger Cloud

Dagger Cloud is under development, but we have just released the first telemetry feature!

:::tip
Ensure you're using Dagger Engine/CLI version `0.2.16` or higher for Dagger Cloud.
:::

To take advantage of this feature, you will need to create an account by following the steps below:

1. Initiate a login process using the CLI with `dagger login`.
2. A new window will open in the browser requesting to sign-up using GitHub.
3. After authenticating, authorize the Dagger GitHub application to finish the process.
4. The browser window will close automatically and your CLI will be authenticated.
5. Verify your upcoming `dagger do` commands include the `--log-level debug` flag.

:::tip
For your run to show up in Dagger cloud it should take this form: `dagger do <action> [flags] --log-level debug`
:::

Once you create an account, after running your project again, you will see the following for each of your runs in a single dashboard:

![Dagger Cloud run URL](/img/dagger-cloud/runs.png)

When you click on a specific run, you can see the following:

- Overview - An overview of the `dagger do` execution results. The presented information will highlight the status of the execution (succeed, failed), contextual information about the environment, and a detailed view of each action with their corresponding log outputs.
- Shareable Run URL - A unique URL only accessible by the owner of the execution as well as some specialized Dagger engineers.
- CUE Plans - The raw execution plan. This provides understanding about how Dagger resolved the action dependencies as well as the CUE evaluation results.
- Actions - All the events involved in the action execution with their corresponding duration and outputs.
- CLI Argument view -  Arguments specified in the CLI when running `dagger do <action> [flags]`.

With this information, we’ve made it easier for you to inspect your runs with a more verbose failure output.

## FAQ

### Who can access the information in my execution URL?

The Dagger team are the only ones who can access the information in the execution URL.

### How can I share my run information with someone else?

You can find the shareable Dagger run URL by clicking on the copy icon where it says “share my run”. The contents of this link are only accessible to our Dagger team and yourself. See example below:

![Dagger Cloud run URL](/img/dagger-cloud/share-url.png)
