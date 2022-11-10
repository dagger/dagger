---
slug: /sdk/python/628797/get-started
---

# Get Started with the Dagger Python SDK

## Introduction

This tutorial teaches you the basics of using Dagger in Python. You will learn how to:

- Install the Python SDK
- Create a Python CI tool to test an application
- Improve the Python CI tool to test the application against multiple Python versions

## Requirements

This tutorial assumes that:

- You have a basic understanding of the Python programming language. If not, [read the Python tutorial](https://www.python.org/about/gettingstarted/).
- You have a Python development environment with Python 3.10 or later. If not, install [Python](https://www.python.org/downloads/).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have a Python application with tests defined and in a [virtual environment](https://packaging.python.org/en/latest/tutorials/installing-packages/#creating-virtual-environments).

:::note
This tutorial creates a CI tool to test your Python application against multiple Python versions. If you don't have a Python application already, clone an existing Python project with a well-defined test suite before proceeding. A good example is the [FastAPI](https://github.com/tiangolo/fastapi) library, which you can clone as below:

```shell
git clone https://github.com/tiangolo/fastapi
```

The code samples in this tutorial are based on the above FastAPI project. If using a different project, adjust the code samples accordingly.
:::

## Step 1: Install the Dagger Python SDK

:::note
The Dagger Python SDK requires [Python 3.10 or later](https://docs.python.org/3/using/index.html). Using a [virtual environment](https://packaging.python.org/en/latest/tutorials/installing-packages/#creating-virtual-environments) is recommended.
:::

Install the Dagger Python SDK in your project's virtual environment using `pip`:

```shell
pip install dagger-io
```

## Step 2: Create a Dagger client in Python

Create a new file named `test.py` and add the following code to it.

```python file=snippets/get-started/step1/test.py
```

This Python stub imports the Dagger SDK and defines an asynchronous function named `test()`. This `test()` function performs the following operations:

- It creates a Dagger client with `dagger.Connection()`. This client provides an interface for executing commands against the Dagger engine. The optional `dagger.Config(log_output=sys.stderr)` configuration displays the output from the Dagger engine.
- It uses the client's `container().from_()` method to initialize a new container from a base image. In this example, the base image is the `python:3.10-slim-buster` image. This method returns a `Container` representing an OCI-compatible container image.
- It uses the `Container.exec()` method to define the command to be executed in the container - in this case, the command `python -V`, which returns the Python version string. The `exec()` method returns a revised `Container` with the results of command execution.
- It retrieves the output stream of the last executed command with the `Container.stdout()` method and prints its contents.

Run the Python CI tool by executing the command below from the project directory:

```shell
python test.py
```

The tool outputs a string similar to the one below.

```shell
Hello from Dagger and Python 3.10.8
```

## Step 3: Test against a single Python version

Now that the basic structure of the CI tool is defined and functional, the next step is to flesh out its `test()` function to actually test the Python application.

Replace the `test.py` file from the previous step with the version below (highlighted lines indicate changes):

```python file=snippets/get-started/step3/test.py
```

The revised `test()` function now does the following:

- It creates a Dagger client with `dagger.Connection()` as before.
- It uses the client's `host().workdir().id()` method to obtain a reference to the current directory on the host. This reference is stored in the `src_id` variable.
- It uses the client's `container().from_()` method to initialize a new container from a base image. This base image is the Python version to be tested against - the `python:3.10-slim-buster` image. This method returns a new `Container` class with the results.
- It uses the `Container.with_mounted_directory()` method to mount the host directory into the container at the `/src` mount point.
- It uses the `Container.with_workdir()` method to set the working directory in the container.
- It chains `Container.exec()` methods to install test dependencies and run tests in the container.
- It uses the `Container.exit_code()` method to obtain the exit code of the last executed command. An exit code of `0` implies successful execution.

:::tip
The `from_()`, `with_mounted_directory()`, `with_workdir()` and `exec()` methods all return a `Container`, making it easy to chain method calls together and create a pipeline that is easy and intuitive to understand.
:::

Run the Python CI tool by executing the command below:

```shell
python test.py
```

The tool tests the application, logging its operations to the console as it works. If all tests pass, it displays the final output below:

```shell
Tests succeeded!
```

## Step 4: Test against multiple Python versions

Now that the Python CI tool can test the application against a single Python version, the next step is to extend it for multiple Python versions.

Replace the `test.py` file from the previous step with the version below (highlighted lines indicate changes):

```python file=snippets/get-started/step4a/test.py
```

This revision of the CI tool does much the same as before, except that it now supports multiple Python versions.

- It defines the test matrix, consisting of Python versions `3.7` to `3.11`.
- It iterates over this matrix, downloading a Python container image for each specified version and testing the source application in that version.

Run the CI tool by executing the command below:

```shell
python test.py
```

The tool tests the application against each version in sequence and displays the following final output:

```shell
Starting tests for Python 3.7
Tests for Python 3.7 succeeded!
Starting tests for Python 3.8
Tests for Python 3.8 succeeded!
Starting tests for Python 3.9
Tests for Python 3.9 succeeded!
Starting tests for Python 3.10
Tests for Python 3.10 succeeded!
Starting tests for Python 3.11
Tests for Python 3.11 succeeded!
All tasks have finished
```

One further improvement is to speed things up by having the tests run concurrently. Here's a revised `test.py` which demonstrates how to do this (highlighted lines indicate changes):

```python file=snippets/get-started/step4b/test.py
```

Run the tool again by executing the command below:

```shell
python test.py
```

Now, the tool performs tests concurrently, with a noticeable difference in the total time required.

## Conclusion

This tutorial introduced you to the Dagger Python SDK. It explained how to install the SDK and use it with a Python package. It also provided a working example of a Python CI tool powered by the SDK, demonstrating how to test an application against multiple Python versions in parallel.

Use the SDK Reference to learn more about the Dagger Python SDK.
