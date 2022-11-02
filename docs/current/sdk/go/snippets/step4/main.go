package main

import (
  "context"
  "fmt"
  "os"
  // highlight-start
  "path/filepath"
  // highlight-end
  "dagger.io/dagger"
)

func main() {
  if len(os.Args) < 2 {
    fmt.Println("Must pass in a Git repository to build")
    os.Exit(1)
  }
  repo := os.Args[1]
  if err := build(repo); err != nil {
    fmt.Println(err)
  }
}

func build(repoUrl string) error {
  fmt.Printf("Building %s\n", repoUrl)

  ctx := context.Background()

  // highlight-start
  // initialize Dagger client
  client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
  if err != nil {
    return err
  }
  defer client.Close()

  // clone repository with Dagger
  repo := client.Git(repoUrl)
  src, err := repo.Branch("main").Tree().ID(ctx)
  if err != nil {
    return err
  }

  // get `golang` image
  golang := client.Container().From("golang:latest")

  // mount cloned repository into `golang` image
  golang = golang.WithMountedDirectory("/src", src).WithWorkdir("/src")

  // define the application build command
  path := "build/"
  golang = golang.Exec(dagger.ContainerExecOpts{
    Args: []string{"go", "build", "-o", path},
  })

  // get reference to build output directory in container
  output, err := golang.Directory(path).ID(ctx)
  if err != nil {
    return err
  }

  // create build/ directory on host
  outpath := filepath.Join(".", path)
  err = os.MkdirAll(outpath, os.ModePerm)
  if err != nil {
    return err
  }

  // get reference to current working directory on the host
  workdir := client.Host().Workdir()

  // write contents of container build/ directory
  // to the host working directory
  _, err = workdir.Write(ctx, output, dagger.HostDirectoryWriteOpts{Path: path})
  // highlight-end
  if err != nil {
    return err
  }

  return nil
}
