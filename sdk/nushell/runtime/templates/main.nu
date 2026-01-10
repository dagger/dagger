#!/usr/bin/env nu
# A generated module for Dagger functions
#
# This module has been generated via dagger init and serves as a reference to
# basic module structure as you get started with Dagger.
#
# Two functions have been pre-created. You can modify, delete, or add to them,
# as needed. They demonstrate usage of arguments and return types using simple
# operations. The functions can be called from the dagger CLI or from one of
# the SDKs.
#
# The first line in this comment block is a short description line and the
# rest is a long description with more detail on the module's purpose or usage,
# if appropriate. All modules should have a short description.

# Import the Dagger API helpers
use /usr/local/lib/dag.nu *

# Returns a container that echoes whatever string argument is provided
# 
# Example: dagger call container-echo --string-arg="hello"
# Returns: Container
export def container-echo [
    string_arg: string  # The string to echo
] {
    # Create a container from alpine, then run echo command
    container from "alpine:latest"
    | container with-exec ["echo", $string_arg]
}

# Returns lines that match a pattern in the files of the provided Directory
#
# Example: dagger call grep-dir --directory-arg=. --pattern="TODO"
# Returns: string
export def grep-dir [
    directory_arg: record  # Directory to search in
    pattern: string        # The pattern to search for
] {
    # Create a container, mount the directory, and run grep
    container from "alpine:latest"
    | container with-directory "/mnt" $directory_arg
    | container with-workdir "/mnt"
    | container with-exec ["grep", "-R", $pattern, "."]
    | container stdout
}
