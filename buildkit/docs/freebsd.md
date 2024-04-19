# Experimental FreeBSD support

Buildkit also has experimental FreeBSD support. The container infrastructure on FreeBSD is still at an early stage, so problems may occur.

Dependencies:

- [runj](https://github.com/samuelkarp/runj)
- [containerd](https://github.com/containerd/containerd)

These dependencies can be installed from the ports tree, or using the `pkg` command:

```csh
% pkg install containerd runj
```

For BuildKit build instructions see [`..github/CONTRIBUTING.md`](../.github/CONTRIBUTING.md).
