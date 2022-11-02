package main

import (
  "context"
  "fmt"
  "os"
  "path/filepath"

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

  // highlight-start
  // define build matrix
  oses := []string{"linux", "darwin"}
  arches := []string{"amd64", "arm64"}
  // highlight-end

  ctx := context.Background()

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

  // get reference to current working directory on the host
  workdir := client.Host().Workdir()

  // highlight-start
  for _, goos := range oses {
    for _, goarch := range arches {
      // create a directory for each os and arch
      path := fmt.Sprintf("build/%s/%s/", goos, goarch)
      outpath := filepath.Join(".", path)
      err = os.MkdirAll(outpath, os.ModePerm)
      if err != nil {
        return err
      }

      // set GOARCH and GOOS in the build environment
      build := golang.WithEnvVariable("GOOS", goos)
      build = build.WithEnvVariable("GOARCH", goarch)

      // build application
      build = build.Exec(dagger.ContainerExecOpts{
        Args: []string{"go", "build", "-o", path},
      })

      // get reference to build output directory in container
      output, err := build.Directory(path).ID(ctx)
      if err != nil {
        return err
      }

      // write contents of container build/ directory
      // to the host working directory
      _, err = workdir.Write(ctx, output, dagger.HostDirectoryWriteOpts{Path: path})
      if err != nil {
        return err
      }
    }
  }
  // highlight-end

  return nil
}
