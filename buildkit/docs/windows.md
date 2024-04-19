<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
<!-- details here: https://github.com/thlorenz/doctoc -->
**Table of Contents**

- [Experimental Windows containers support](#experimental-windows-containers-support)
  - [Quick start guide](#quick-start-guide)
    - [Platform requirements](#platform-requirements)
    - [Setup Instructions](#setup-instructions)
    - [Example Build](#example-build)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Experimental Windows containers support

As from `v0.13`, Buildkit now has experimental support for Windows containers (WCOW). Both `buildctl.exe` and `buildkitd.exe` binaries are being released for testing purposes.

We will apprecate any feedback by [opening an issue here](https://github.com/moby/buildkit/issues/new), as we stabilize the product, especially `buildkitd.exe`.

## Quick start guide

### Platform requirements

- Architecture: `amd64`, `arm64` (binaries available but not officially tested yet).
- Supported OS: Windows Server 2019, Windows Server 2022, Windows 11.
- Base images: `ServerCore:ltsc2019`, `ServerCore:ltsc2022`, `NanoServer:ltsc2022`. See the [compatibility map here](https://learn.microsoft.com/en-us/virtualization/windowscontainers/deploy-containers/version-compatibility?tabs=windows-server-2019%2Cwindows-11#windows-server-host-os-compatibility).

### Setup Instructions

**Dependency:** `containerd` v1.7.7+

> **NOTE:** all these requires running as admin (elevated) on a PowerShell terminal.

Make sure that `Containers` feature is enabled. (_`Microsoft-Hyper-V` is a bonus but not necessarily needed for our current guide. Also it's depended on your virtualization platform setup._) Run:

```powershell
Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V, Containers -All
```

You will be asked to restart your machine, do so, and then continue with the rest of the steps. No other restart needed.

1. Setup `containerd` by following [the setup instructions here](https://github.com/containerd/containerd/blob/main/docs/getting-started.md#installing-containerd-on-windows). (_Currently, we only support the `containerd` worker_.)
1. Start the `containerd` service, if not yet started.
1. Download and extract:
    ```powershell
    $version = "v0.13.0-rc2" # specify the release version, v0.13+
    $arch = "amd64" # arm64 binary available too
    curl.exe -LO https://github.com/moby/buildkit/releases/download/$version/buildkit-$version.windows-$arch.tar.gz
    # there could be another `.\bin` directory from containerd instructions
    # you can move those
    mv bin bin2
    tar.exe xvf .\buildkit-$version.windows-$arch.tar.gz
    ## x bin/
    ## x bin/buildctl.exe
    ## x bin/buildkitd.exe
    ```
1. Setup `buildkit` binaries:
    ```powershell
    # after the binaries are extracted in the bin directory
    # move them to an appropriate path in your $Env:PATH directories or:
    Copy-Item -Path ".\bin" -Destination "$Env:ProgramFiles\buildkit" -Recurse -Force
    # add `buildkitd.exe` and `buildctl.exe` binaries in the $Env:PATH
    $Path = [Environment]::GetEnvironmentVariable("PATH", "Machine") + `
        [IO.Path]::PathSeparator + "$Env:ProgramFiles\buildkit"
    [Environment]::SetEnvironmentVariable( "Path", $Path, "Machine")
    $Env:Path = [System.Environment]::GetEnvironmentVariable("Path","Machine") + ";" + `
        [System.Environment]::GetEnvironmentVariable("Path","User")
    ```
1. Start `buildkitd.exe`, you should see something similar to:

    ```
    PS C:\> buildkitd.exe
    time="2024-02-26T10:42:16+03:00" level=warning msg="using null network as the default"
    time="2024-02-26T10:42:16+03:00" level=info msg="found worker \"zcy8j5dyjn3gztjv6gv9kn037\", labels=map[org.mobyproject.buildkit.worker.containerd.namespace:buildkit org.mobyproject.buildkit.worker.containerd.uuid:c30661c1-5115-45de-9277-a6386185a283 org.mobyproject.buildkit.worker.executor:containerd org.mobyproject.buildkit.worker.hostname:[deducted] org.mobyproject.buildkit.worker.network: org.mobyproject.buildkit.worker.selinux.enabled:false org.mobyproject.buildkit.worker.snapshotter:windows], platforms=[windows/amd64]"
    time="2024-02-26T10:42:16+03:00" level=info msg="found 1 workers, default=\"zcy8j5dyjn3gztjv6gv9kn037\""
    time="2024-02-26T10:42:16+03:00" level=warning msg="currently, only the default worker can be used."
    time="2024-02-26T10:42:16+03:00" level=info msg="running server on //./pipe/buildkitd"
    ```
1. In another terminal (still elevated), try out a `buildctl` command to test that the setup is good:
    ```
    PS> buildctl debug info
    BuildKit: github.com/moby/buildkit v0.0.0+unknown
    ```
    > **NOTE:** the version is `v0.0.0+unknown` since this is still a _release candidate (RC)_.

### Example Build

Now that everything is setup, let's build a [simple _hello world_ image](https://github.com/docker-library/hello-world/blob/master/amd64/hello-world/nanoserver-ltsc2022/Dockerfile).

1. Create a directory called `sample_dockerfile`:
    ```powershell
    mkdir sample_dockerfile
    cd sample_dockerfile
    ```

1. Inside it, add files `dockerfile` and `hello.txt`:
    ```powershell
    Set-Content Dockerfile @"
    FROM mcr.microsoft.com/windows/nanoserver:ltsc2022
    USER ContainerAdministrator
    COPY hello.txt C:/
    RUN echo "Goodbye!" >> hello.txt
    CMD ["cmd", "/C", "type C:\\hello.txt"]
    "@

    Set-Content hello.txt @"
    Hello from buildkit!
    This message shows that your installation appears to be working correctly.
    "@
    ```
    > **NOTE:** For a consistent experience, just use the front-slashes `/` for paths, e.g. `C:/` instead of `C:\`. We are working to fix this, see [#4696](https://github.com/moby/buildkit/issues/4696).

1. Build and push to your registry (or set to `push=false`). For Docker Hub, make sure you've done `docker login`. See more details on registry configuration [here](../README.md#imageregistry)

    ```powershell
    buildctl build `
    --frontend=dockerfile.v0 `
    --local context=. \ `
    --local dockerfile=. `
    --output type=image,name=docker.io/<your_username>/hello-buildkit,push=true
    ```

    You should see a similar output:

    ```
    [+] Building 5.6s (8/8) FINISHED
    => [internal] load build definition from Dockerfile                                                                                    0.0s
    => => transferring dockerfile: 213B                                                                                                    0.0s
    => [internal] load metadata for mcr.microsoft.com/windows/nanoserver:ltsc2022                                                          0.2s
    => [internal] load .dockerignore                                                                                                       0.0s
    => => transferring context: 2B                                                                                                         0.0s
    => CACHED [1/3] FROM mcr.microsoft.com/windows/nanoserver:ltsc2022@sha256:64b22e42a69ebcdb86e49bf50780b64156431a508f7f06ac3050c71920f  0.1s
    => => resolve mcr.microsoft.com/windows/nanoserver:ltsc2022@sha256:64b22e42a69ebcdb86e49bf50780b64156431a508f7f06ac3050c71920fe57b7    0.1s
    => [internal] load build context                                                                                                       0.0s
    => => transferring context: 133B                                                                                                       0.0s
    => [2/3] COPY hello.txt  C:/                                                                                                           0.3s
    => [3/3] RUN echo Goodbye! >> C:/hello.txt                                                                                             1.9s
    => exporting to image                                                                                                                  2.7s
    => => exporting layers                                                                                                                 1.3s
    => => exporting manifest sha256:625a648ad14e6359a8bfa53c676985922834f1ad911e4c38cefac6e0e8e50c9e                                       0.0s
    => => exporting config sha256:cee4c434ec5cd7fa458ec71d5ca423948dad91ba1c84a7cd75df576ea4a3b7e8                                         0.0s
    => => naming to docker.io/profnandaa/hello-buildkit                                                                                    0.0s
    => => pushing layers                                                                                                                   1.1s
    => => pushing manifest for docker.io/profnandaa/hello-buildkit:latest@sha256:625a648ad14e6359a8bfa53c676985922834f1ad911e4c38cefac6e0  0.2s
    ```

> **NOTE:** After pushing to the registry, you can use your image with any other clients to spin off containers, e.g. `docker run`, `ctr run`, `nerdctl run`, etc.
