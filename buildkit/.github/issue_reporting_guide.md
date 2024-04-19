# Issue reporting guide

* [Security issues](#security-issues)
* [Search for an existing issue](#search-for-an-existing-issue)
* [Reproducible test case](#reproducible-test-case)
* [Describe your environment](#describe-your-environment)
* [Reporting deadlock](#reporting-deadlock)
* [Reporting panics/error returns](#reporting-panicserror-returns)
* [Gathering more debugging data](#gathering-more-debugging-data)
* [Regressions](#regressions)
* [Debugging issues that only reproduce in the live environment](#debugging-issues-that-only-reproduce-in-the-live-environment)
* [Follow the progress of your issue](#follow-the-progress-of-your-issue)
* [Try fixing your issue yourself](#try-fixing-your-issue-yourself)
* [Additional reading](#additional-reading)

## Security issues

Do NOT report security issues publicly. See [https://github.com/moby/buildkit/security/policy](https://github.com/moby/buildkit/security/policy) 

## Search for an existing issue

Before creating a new issue, search the repository to see if a similar issue has already been reported before. You can also try Google, as GitHub search is not that great.

If you find an existing issue, there is still a chance that you can provide additional information there. It may be missing a lot of information you can provide that could help contributors fix the problem quicker. This guide should give you enough information to evaluate if the existing report could be improved.

You can use the reactions feature of GitHub to signal that you have also hit the same problem. Leaving “me too” comments on the issue without any new useful information isn’t really helpful. If you notice that the issue is not properly labeled or should be in the milestone (see [Follow the progress of your issue](#follow-the-progress-of-your-issue)) then you can leave a comment, notifying the maintainers. If the issue you find is already closed, it is usually better to create a new one instead of leaving a new comment on closed issue.

If you are not sure if your problem is the same as another report, then the best approach is to open a new issue and link to a previous one so a maintainer can decide if the new issue is a duplicate or not.

## Reproducible test case

The best way for you to drastically improve the chances of your issue getting resolved quickly is to provide a reproducible test case that demonstrates the problem. Maintainers need to choose where they spend their time, and the last thing they want is to spend a day chasing an issue only to find out in the end that there was no problem in the code or that whatever they try, there is no way they can validate that description in the report is correct. In prioritization, we need to look at the reports that can make the biggest user impact in minimal development time.

Ideally, your reproducer runs in containers. This is easiest for the maintainers to replicate, who may not have the same setup as you do.

Depending on how complex the problem you are reporting is the test case might be just some commands in the description or a separate script. In some cases where you need multiple files it makes sense to create a separate “reproducer repository” under your GitHub account that can be cloned to run the your scripts.

`docker buildx create` command makes it easy to run BuildKit in a container. This way you can control that it starts empty, and you can build up your state precisely. If you need buildkit in docker daemon can also run in `dind` image [https://hub.docker.com/_/docker](https://hub.docker.com/_/docker). For regular script containers you can just `docker run` and there is no need for extra sandbox in `dind`.

If you need a registry, you can run a local one inside a container with [https://hub.docker.com/_/registry](https://hub.docker.com/_/registry) image.

Often your issue will appear when running your complicated production build that has lots of moving parts. Try to simplify your case until you find the part that actually makes a difference for the issue you are reporting, and report the minimal testcase that you can come up with. Even if you need a specific type of build you may not need the exact features. For example, you might not need a registry (that makes reproducible test case harder) but same issue could also reproduce with OCI tarball export (`-o type=oci` ) that has a very similar code path but is much easier to call.

If you have provided a reproducible test case with your issue, a maintainer will reproduce and confirm if it is a problem. Then the maintainer marks the issue with “bug” and “confirmed” labels, indicating that the report is correct and ready for anyone to work on it.

Sometimes the issue doesn’t reproduce every time. If you can put together a test case the only shows the problem 10-20% of times it is invoked it can still be very helpful for the maintainers.

If you can’t put together a reproducible test case, you should still open an issue and try to provide as much other information as you can.

## Describe your environment

In addition to describing the problem make sure to provide enough details about the environment where you have confirmed the issue is appearing.

There are lots of ways to run BuildKit, first make it clear which way you are using. Are you using `docker build` , a specific driver of `docker buildx` , running `buildkitd` or `buildctl` manually, in kubernetes, etc. 

Sometimes it is not completely clear if the issue belongs in `moby/buildkit` , `docker/buildx` or even `moby/moby` repository. If you are unsure you can start from either BuildKit or Buildx and maintainers, feel free to report the issue here. Maintainers will re-route it if necessary. BuildKit is where your builds actually run, Buildx covers a Docker CLI experience and BuildKit deployment with drivers.

Provide versions of the tools you used:

- Using Buildkitd/Buildctl  `buildctl --version` `buildkitd version`
- Using Docker Buildx `docker buildx version` and `docker buildx inspect` to return info about your current builder instance. If you run `docker build` then also report this.
- If the issue is specific to Docker Engine embedded BuildKit or `docker buildx` Docker driver then report `docker version` and `docker info`

If you are running an older version of BuildKit then ideally, you should try to reproduce the problem with the latest version before reporting it.

The easiest way to test the latest BuildKit release is with the Docker Buildx container driver:

```bash
docker buildx create --name buildkit-latest --bootstrap
export BUILDX_BUILDER=buildkit-latest
docker buildx inspect
```

If you also want to check if the issue still appears in the `master` development branch you can run:

```bash
docker buildx build --load -t moby/buildkit:dev "https://github.com/moby/buildkit.git"
docker buildx create --name buildkit-dev --driver-opt image=moby/buildkit:dev --bootstrap
export BUILDX_BUILDER=buildkit-dev
docker buildx inspect
```

To check if a specific existing PR is fixing the problem you can replace the first line with:

```bash
docker buildx build --load -t moby/buildkit:dev "https://github.com/moby/buildkit.git#pull/NR/head"
```

If your issue requires a specific environment or you have only encountered it in a specific environment then make sure to describe that.

This would be for example specific BuildKit configurations:

- Rootless mode
- Non-default image store (e.g. containerd with a custom snapshotter)
- Special (insecure/mirror) registry configuration
- Using containerd worker instead of default OCI one
- Custom CNI configuration
- Specific Dockerfile version of custom frontend

Or specific execution environment:

- Docker Desktop or other Docker distro
- AWS/EBS
- Specific kernel
- Custom restricted environment
- Uncommon filesystem

If you are running in a specific environment try to test if that environment actually makes a difference for your issue. Eg. if you are using rootless but the issue you are reporting also shows in rootful mode then don’t report it as a rootless issue.

If your issue requires a specific stable infrastructure some options you can try is to use GitHub Codespaces (should also be same infrastructure that is used in GitHub Actions). If needed we can run your reproducer also in Codespaces environment. It is also relatively easy for maintainers to run a specific EC2 instance if it is needed for reproduction.

## Reporting deadlock

A deadlock or a hang is a situation where BuildKit locks up and the ongoing request does not progress anymore. It may also block some further requests.

In such cases it is paramount that you report the issue with a stacktrace. From the stacktrace, maintainers can see the code path where the program is stuck. Without it, it is mostly blind guesswork.

Often a deadlock appears on a live system and is very hard to replicate after you have restarted the process. This is why it is important to capture the traces as soon as you see the issue, as you might not have a chance to see it again.

There are two ways to capture the stacktrace.

All BuildKit/Docker tools are written in Go programming language and Go has a builtin way for printing stacktrace by sending `SIGQUIT` signal to the process. This will print the stacktrace of all Goroutines to Stderr and close the program. This method can be used for any program, including `buildctl` or `buildx` .

Additionally, `buildkitd` supports running a Debug server that can be used to get stacktraces (and other debug information) from running process without killing it. Debug server can be started by setting `--debugaddr` flag, eg. `--debugaddr 127.0.0.1:6060` . In Buildx such flags can be set in `docker buildx create --buildkitd-flags '--debugaddr 127.0.0.1:6060'` . 

That server handles many debug request, you can find full list from [https://github.com/moby/buildkit/blob/master/cmd/buildkitd/debug.go#L15](https://github.com/moby/buildkit/blob/master/cmd/buildkitd/debug.go#L15) . To capture stacktraces of a process run:

```bash
curl "http://127.0.0.1:6060/debug/pprof/goroutine?debug=2"
```

This will return stacktraces in text format. The server also supports Goroutine profile in pprof format but text format is preferred and contains most information.

Always report the full stacktrace. Do not try to only report the first lines or the part that you think is important. If you are worried this might leak some private data (very unlikely in this case), you can ask private way to send files to a maintainer directly.

## Reporting panics/error returns

In the case where you run a command and instead of succeeding it unexpectedly returns and error or a panic it is also possible to capture the stacktrace that can help maintainers to investigate your report.

A panic is special type of runtime error that crashed your program (either buildkitd or client). Panics are automatically printed with stacktrace that you should include in your report in full form.

Sometimes when you are running BuildKit in a container with Docker Buildx and it panics it can look from Buildx side that the request just dropped (sometimes `EOF` error is shown) or was left hanging. In that case use the `docker logs` command with buildkit container name to capture the panic stacktrace from the container logs.

If you receive a build error, these also usually have captured a stacktrace but it is not shown by default. In `docker buildx` you can run your command with extra `--debug` flag `docker --debug buildx` instead that will enable debug logs and also print stacktrace when error appears. The stacktrace will contain information from client, daemon and BuildKit frontend so make sure you copy it fully. Additionally if you enable debug logs for `buildkitd` with `--debug` flags then error stack traces are also printed to the `buildkitd` logs.

In addition to stacktrace, make sure to also always include the full error message as it is printed to you.

## Gathering more debugging data

If you reach a condition where it looks to you the BuildKit is not behaving correctly, it is a good idea to check if there is anything interesting printed in the daemon logs and include it in your report.

Debug logs that print additional debugging messages about the execution of BuildKit can be enabled with `buildkitd --debug` or `dockerd -D` if running BuildKit via Docker engine.

Especially, when reporting issues about storage/prune , communication to registry, build steps running in containers you should always try to include daemon logs in debug mode.

BuildKit can be set to a special debug mode where it prints all decisions about the build graph solve process by setting `BUILDKIT_SCHEDULER_DEBUG=1` . This is advanced usage and should only be used if you already have some understanding how scheduler component relates to your issue or if maintainer has asked you to rerun your case with this flag enabled.

If you have some coding experience, it is actually quite easy to run a custom version of BuildKit with your own custom debug messages added. If you have located interesting part of the code (for example searching where the error message you are seeing is coming from) you can add the additional logs and then run:

```bash
docker buildx build -t moby/buildkit:dev --load .
docker buildx create --name buildkit-dev --driver-opt image=moby/buildkit:dev --buildkitd-flags '--debug' --bootstrap
export BUILDX_BUILDER=buildkit-dev
docker buildx inspect

...

# get the logs 
docker logs buildx_buildkit_buildkit-dev0

# when you are done
docker buildx rm buildkit-dev
```

For certain types of errors, for example processes unexpectedly crashing it might be interesting to also look at logs from other tools running on the system that might be conflicting with buildkit. `dmesg` or `unlimit -a` (`cat /proc/<buildkitd pid>/limits`) might show something interesting.

When reporting your debug logs make sure they don’t contain information that you would consider private. In that case try to redact the private information instead of reporting a small selection of the logs. If needed you can ask a private way to send your logs directly to a maintainer.

## Regressions

Regressions are bugs where unexpected behavior change has happened between BuildKit releases (or between release and development branch) or feature that was working in old version does not work anymore. Regression reports are especially common and expected against candidate releases (-rc).

When you are reporting a regression, mark it clearly in the issue title, eg.  “[v0.13.0-rc1] panic when running X”. This helps maintainers to see it quicker and prioritize properly.

If you have determined such a case it would be very helpful if you can determine the exact commit that broke the behavior. This almost always needs to be done anyway to understand the full scope and extent of the regression and can save maintainers time or any possible issues when maintainers can’t replicate the behavior you are seeing.

Offending commit can be found using `git bisect` command [https://stackoverflow.com/questions/4713088/how-to-use-git-bisect](https://stackoverflow.com/questions/4713088/how-to-use-git-bisect) .

There are more advanced ways to use `git bisect` (that automatically find the commit using a script) but basic workflow would be.

```bash
# clean checkout
git bisect start
git bisect good <known good commit SHA or tag/branch>
git bisect bad <known good commit SHA or tag/branch>

# this does a checkout to a candidate commit

# run your steps with buildkit from that step

docker buildx build -t moby/buildkit:dev --load .
docker buildx create --name buildkit-dev --driver-opt image=moby/buildkit:dev --buildkitd-flags '--debug' --bootstrap
export BUILDX_BUILDER=buildkit-dev

# your test code 

# when you are done
unset BUILDX_BUILDER
docker buildx rm buildkit-dev

# if your test code behaved correctly then
git bisect good
# otherwise
git bisect bad

# this will checkout another candidate commit that you can test again

# Eventually this will print you a message about which commit was the first one that
# introduced the issue.
```

## Debugging issues that only reproduce in the live environment

Sometimes BuildKit is in a bad state and behaves incorrectly, but there is no way to reproduce it and restarting process would make the issue go away.

In that case the best option is to use interactive debugger attached to the `buildkitd` process. This involves some understanding of Go tooling and BuildKit codebase.

[Delve](https://github.com/go-delve/delve) is a debugger for Go that can be used for such tasks.

BuildKit image can be built together with Delve by setting `--build-arg BUILDKIT_DEBUG=1` when building the image. In most cases you will not know you need Delve though before you already have a faulty instance. In that case you can use `docker cp` to copy static binary of Delve into the BuildKit container and then invoke it from `docker exec` session.

In order to attach to running `buildkitd` instance, run:

```bash
dlv attach <buildkitd-pid> $(which buildkitd)
```

Refer to Delve documentation for how to use it. You need to add breakpoints to interesting lines of code or functions and after the debugger has stopped in such lines you can follow the execution path, see contents of variables etc. Capture this information for the report.

For best debugging experience you may need to disable optimizations such as function inlining when building `buildkitd` binary for interactive debugging. This may have some(but not drastical) impact of performance of the binary. [https://github.com/moby/buildkit/blob/d736391494e1159883f5a8b4757f4dfd290462de/Dockerfile#L118](https://github.com/moby/buildkit/blob/d736391494e1159883f5a8b4757f4dfd290462de/Dockerfile#L118)

## Follow the progress of your issue

We can not provide you any guarantees of when the issue you have reported will be fixed. What you can do is ask that your issue is properly tracked:

- If you reported a bug it should be marked as such. Usually, we only mark issues as “bug” if we have independently verified the claims, or if the data provided is enough to confirm that something is definitely wrong.
- If you provided a reproducible test case then the issue should be marked as “confirmed”, signaling that maintainers have verified/accepted it and anyone could start working on it.
- If issue requires more data from original reporter (or anyone else how can reproduce) it should be marked as “needs-more-info”.
- If an issue is a regression from the previous release it should automatically be high priority and added to the next patch release milestone.
- If the issue looks critical enough ask it to be included in the next milestone or next patch release milestone. In addition to regressions these would usually need to be issues in the critical/common path, panics etc. We will work on defining this more clearly. Our guidelines for issue prioritization can be found in [here](/PROJECT.md#issue-categorization-guidelines).

If you notice that a specific maintainer has worked on the feature your report is about before you may mention them directly on your issue. You can also ping specific people when you think it has been unreasonably long time since the last answer.

## Try fixing your issue yourself

We appreciate if you can take initiative for fixing your issue yourself and can help you through the process. Start by reading through contributing guide [https://github.com/moby/buildkit/blob/master/.github/CONTRIBUTING.md](https://github.com/moby/buildkit/blob/master/.github/CONTRIBUTING.md) and relevant developer docs [https://github.com/moby/buildkit/tree/master/docs/dev](https://github.com/moby/buildkit/tree/master/docs/dev) .

For bigger items please confirm your proposed design with the maintainers first to make sure the review process goes smoothly.

If you have questions maintainers can answer them in the issue or in #buildkit channel at Docker community slack.

You can also ask to be assigned on the issue to avoid any possible duplicate work with anyone else.

## Additional reading

[BuildKit project process guide](/PROJECT.md)

[Code of conduct](/.github/CODE_OF_CONDUCT.md)

[Open Source Etiquette](https://opensource.how/etiquette/)
