A module to attach a dockerd service to a container

Example:

```go
myContainerWithDocker, err := dag.Dockerd().Attach(myContainer)
```

