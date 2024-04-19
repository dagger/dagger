# Distributed Build with Consistent Hashing

Demo for efficiently using BuildKit daemon-local cache with multi-node cluster

## Deploy

```console
$ kubectl apply -f ../statefulset.rootless.yaml
$ kubectl scale --replicas=10 statefulset/buildkitd
```

## Consistent hashing

Define the key string for consistent hashing.

For example, the key can be defined as `<REPO NAME>:<CONTEXT PATH>`, e.g.
`github.com/example/project:some/directory`.


Then determine the pod that corresponds to the key:
```console
$ go build -o consistenthash .
$ pod=$(./show-running-pods.sh | consistenthash $key)
```

You can connect to the pod using `export BUILDKIT_HOST=kube-pod://$pod`.
