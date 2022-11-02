package main

import (
  "context"
  "fmt"
  "os"
  "path/filepath"

  "dagger.io/dagger"
  // highlight-start
  "golang.org/x/sync/errgroup"
  // highlight-end
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

  // define build matrix
  oses := []string{"linux", "darwin"}
  arches := []string{"amd64", "arm64"}
  goVersions := []string{"1.18", "1.19"}

  ctx := context.Background()
  // highlight-start
  g, ctx := errgroup.WithContext(ctx)
  // highlight-end

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

  // get reference to current working directory on the host
  workdir := client.Host().Workdir()

  for _, version := range goVersions {
    // get `golang` image for specified Go version
    imageTag := fmt.Sprintf("golang:%s", version)
    golang := client.Container().From(imageTag)
    // mount cloned repository into `golang` image
    golang = golang.WithMountedDirectory("/src", src).WithWorkdir("/src")

    for _, goos := range oses {
      for _, goarch := range arches {
        // highlight-start
        goos, goarch, version := goos, goarch, version
        g.Go(func() error {
          // create a directory for each os, arch and version
          path := fmt.Sprintf("build/%s/%s/%s/", version, goos, goarch)
          outpath := filepath.Join(".", path)
          err = os.MkdirAll(outpath, os.ModePerm)
          if err != nil {
            return err
          }

          // set GOARCH and GOOS in the build environment
          build := golang.WithEnvVariable("GOOS", goos)
          build = build.WithEnvVariable("GOARCH", goarch)
          build = build.Exec(dagger.ContainerExecOpts{
            Args: []string{"go", "build", "-o", path},
          })

          // build application
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
          return nil
        })
        // highlight-end
      }
    }
  }
  // highlight-start
  if err := g.Wait(); err != nil {
    return err
  }
  // highlight-end
  return nil
}
