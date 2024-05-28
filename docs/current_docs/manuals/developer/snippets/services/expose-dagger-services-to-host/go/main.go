package main

type MyModule struct{}

// Start and return an HTTP service
func (m *MyModule) HttpService() *Service {
	return dag.Container().
		From("python").
		WithWorkdir("/srv").
		WithNewFile("index.html", ContainerWithNewFileOpts{
			Contents: "Hello, world!",
		}).
		WithExec([]string{"python", "-m", "http.server", "8080"}).
		WithExposedPort(8080).
		AsService()
}
