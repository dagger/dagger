---
slug: /1229/empty-buildkit-cache
displayed_sidebar: '0.2'
---

# How to empty BuildKit's cache ?

There are two ways of emptying the BuildKit cache:

- Run your action with the `--no-cache` option:

```console
dagger do <your-action> --no-cache
```

- Stop and remove the buildkitd container then remove its associated volume:

```console
docker stop dagger-buildkitd ; docker rm dagger-buildkitd ; docker volume rm dagger-buildkitd
```

In 99.9% of the cases, the first solution is enough
