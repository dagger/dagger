package main

import (
	"go.dagger.io/dagger/cmd/dagger/cmd"
)

func main() {
	cmd.Execute()
}

// func main() {
// 	cmd.Execute()
// }

// func main() {
// 	ctx := context.Background()
// 	d, err := dagger.Connect(ctx)
// 	if err != nil {
// 		panic(err)
// 	}

//		plaintext, err := d.Container().From("alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3").
//			Exec(dagger.ContainerExecOpts{
//				Args: []string{"test", "foo", "=", "foo"},
//			}).Stdout().Contents(ctx)
//		if err != nil {
//			panic(err)
//		}
//		fmt.Println(plaintext)
//	}
// func main() {
// 	ctx := context.Background()
// 	d, err := dagger.Connect(ctx)
// 	dockerfilepath := "mydockerfile"

// 	buildcontext := d.Directory().WithNewFile(dockerfilepath, dagger.DirectoryWithNewFileOpts{Contents: "FROM alpine"})

// 	build := d.Container().Build(buildcontext, dagger.ContainerBuildOpts{Dockerfile: dockerfilepath})

// 	buildid, err := build.ID(ctx)
// 	if err != nil {
// 		panic(err)
// 	}

//		fmt.Println(buildid)
//	}

// package main

// import (
// 	"context"
// 	"fmt"
// 	"os"

// 	"dagger.io/dagger"
// )

// func main() {
// 	ctx := context.Background()
// 	fmt.Println("Connecting")
// 	d, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
// 	if err != nil {
// 		panic(err)
// 	}

// 	fmt.Println("Getting EnvVariables from nginx:1.23.2")
// 	nginx := d.Container().From("nginx:1.23.2")

// 	// environment, err := nginx.Exec(dagger.ContainerExecOpts{Args: []string{"env"}}).Stdout().Contents(ctx)
// 	// fmt.Println(environment)
// 	vars, err := nginx.EnvVariables(ctx)
// 	if err != nil {
// 		fmt.Println(err)
// 	}

// 	fmt.Println("Printing", len(vars), "environment variables")
// 	for _, env := range vars {
// 		name, err := env.Name(ctx)
// 		if err != nil {
// 			panic(err)
// 		}
// 		val, err := env.Value(ctx)
// 		if err != nil {
// 			panic(err)
// 		}
// 		fmt.Println(name, "=", val)
// 	}
// }
