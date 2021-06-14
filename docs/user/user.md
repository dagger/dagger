---
slug: /user
---

# User Manual

In this part, you will learn how to deploy a simple [React](https://reactjs.org/) application. This is an introduction before you [learn how to program dagger](/programming).

You will learn how to use the most common dagger commands.

## Before we start

First, you'll need to make sure [you have installed dagger on your local machine](/install).

## Let's deploy our first application

**Step 1**: Clone the example repository

```sh
git clone https://github.com/dagger/examples.git
```

**Step 2**: Go the todoapp directory

`todoapp` is a simple Todo-list application written in Javascript using [React](https://reactjs.org/).

Go to the app directory:

```sh
cd ./examples/todoapp
```

**Step 3**: Decrypt the inputs

The example app contains encrypted secrets and other pre-configured inputs, here is how to decrypt them:

```sh
dagger input list || curl -sfL https://releases.dagger.io/examples/key.txt >> ~/.config/dagger/keys.txt
```

**Step 4**: Deploy!

```sh
dagger up
```

At the end of the deploy, you should see a list of outputs. There is one that is named `url`. This is the URL where our app has been deployed. If you go to this URL, you should see your application live!

## Change some code and re-deploy

This repository is already configured to deploy the code in the directory `./todoapp`, so you can change some code (or replace the app code with another react app!) and re-run the following command to re-deploy when you want your changes to be live:

```sh
dagger up
```

## Under the hood

This example showed you how to deploy and develop on an application that is already configured with dagger. Now, let's learn a few concepts to help you understand how this was put together.

### The Environment

An Environment holds the entire deployment configuration.

You can list existing environment from the `./todoapp` directory:

```sh
dagger list
```

You should see an environment named `s3`. You can have many environments within your app. For instance one for `staging`, one for `dev`, etc...

Each environment can have different kind of deployment code. For example, a `dev` environment can deploy locally, a `staging` environment can deploy to a remote infrastructure, and so on.

### The plan

The plan is the deployment code, that includes the logic to deploy the local application to an AWS S3 bucket. From the `todoapp` directory, you can list the code of the plan:

```sh
ls -l .dagger/env/s3/plan/
```

Any code change to the plan will be applied during the next `dagger up`.

### The inputs

The plan can define one or several `inputs` in order to take some information from the user. Here is how to list the current inputs:

```sh
dagger input list
```

The inputs are persisted inside the `.dagger` directory and pushed to your git repository. That's why this example application worked out of the box.

### The outputs

The plan defines one or several `outputs`. They can show useful information at the end of the deployment. That's how we read the deploy `url` at the end of the deployment. Here is the command to list all inputs:

```sh
dagger output list
```

## What's next?

At this point, you have deployed your first application using dagger and learned some dagger commands. You are now ready to [learn more about how to program dagger](/programming).
