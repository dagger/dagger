# kubectl

The `image+kubectl://` driver behaves much like the existing image drivers backed by e.g. `docker`, except that instead of creating a Dagger engine in a Docker container or similar, it creates one in a Kubernetes Pod.

Once the Pod is provisioned, the driver re-uses the existing `kube-pod://` driver to connect to it.

The `image+kubectl//` supports most of the same configuration as the other `image+<runtime>://` drivers, with a couple of small exceptions:

- `DAGGER_LEAVE_OLD_ENGINE=<bool>` - identical behavior.
- `DAGGER_CLOUD_TOKEN=<token>` - identical behavior, except that it gets stored in an intermediate Secret before being injected into the Pod's environment.
- `?cleanup=<bool>` - identical behavior.
- `?container=<name>` - renamed to `?pod=<name>` with identical behavior.
- added `?context=<context>` to create Dagger engine Pods via Kubernetes contexts other than the current context.
- added `?namespace=<namespace>` to create Dagger engine Pods in Namespaces other than the default Namespace for the targeted context.
- `~/.config/dagger/engine.toml` - identical behavior, except that it gets stored in an intermediate ConfigMap before being mounted into the Pod instead of using a bind mount.
- `~/.config/dagger/ca-certificates` - identical behavior, except that it gets stored in an intermediate Secret before being mounted into the Pod instead of using a bind mount.
