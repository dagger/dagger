package main

import (
  "context"
  "fmt"
  "os"

  "dagger.io/dagger"
)

func main() {
  ctx := context.Background()

  // create Dagger client
  client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))

  if err != nil {
    panic(err)
  }
  defer client.Close()

  // create Redis service container
  redisSrv := client.Container().
    From("redis").
    WithExposedPort(6379).
    WithExec(nil)

  // create Redis client container
  redisCLI := client.Container().
    From("redis").
    WithServiceBinding("redis-srv", redisSrv).
    WithEntrypoint([]string{"redis-cli", "-h", "redis-srv"})

  // send ping from client to server
  ping := redisCLI.WithExec([]string{"ping"})

  val, err := ping.
    Stdout(ctx)

  if err != nil {
    panic(err)
  }

  fmt.Println(val)
}
