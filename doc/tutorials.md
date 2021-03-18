# Dagger Tutorials

## Compute a test configuration

Currently `dagger` can only do one thing: compute a configuration with optional inputs, and print the result.

If you are confused by how to use this for your application, that is normal: `dagger compute` is a low-level command
which exposes the naked plumbing of the Dagger engine. In future versions, more user-friendly commands will be available
which hide the complexity of `dagger compute` (but it will always be available to power users, of course!).

Here is an example command, using an example configuration:

```
$ dagger compute ./examples/simple --input-string www.host=mysuperapp.com --input-dir www.source=.
```
