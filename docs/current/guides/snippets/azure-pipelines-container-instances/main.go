package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"dagger.io/dagger"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerinstance/armcontainerinstance/v2"
)

func main() {

	// configure container group, name and location
	containerName := "my-app"
	containerGroupName := "my-app"
	containerGroupLocation := "eastus"
	resourceGroupName := "my-group"

	// check for required variables in host environment
	vars := []string{"DOCKERHUB_USERNAME", "DOCKERHUB_PASSWORD", "AZURE_SUBSCRIPTION_ID", "AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET"}
	for _, v := range vars {
		if os.Getenv(v) == "" {
			log.Fatalf("Environment variable %s is not set", v)
		}
	}

	// initialize Dagger client
	ctx := context.Background()
	daggerClient, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer daggerClient.Close()

	// set registry password as Dagger secret
	dockerHubPassword := daggerClient.SetSecret("dockerHubPassword", os.Getenv("DOCKERHUB_PASSWORD"))

	// get reference to the project directory
	source := daggerClient.Host().Directory(".", dagger.HostDirectoryOpts{
		Exclude: []string{"ci", "node_modules"},
	})

	// get Node image
	node := daggerClient.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).
		From("node:18")

	// mount source code directory into Node image
	// install dependencies
	// set entrypoint
	c := node.WithDirectory("/src", source).
		WithWorkdir("/src").
		WithExec([]string{"cp", "-R", ".", "/home/node"}).
		WithWorkdir("/home/node").
		WithExec([]string{"npm", "install"}).
		WithEntrypoint([]string{"npm", "start"})

	// publish image
	dockerHubUsername := os.Getenv("DOCKERHUB_USERNAME")
	addr, err := c.WithRegistryAuth("docker.io", dockerHubUsername, dockerHubPassword).
		Publish(ctx, fmt.Sprintf("%s/my-app", dockerHubUsername))
	if err != nil {
		panic(err)
	}

	// print ref
	fmt.Println("Published at:", addr)

	// initialize Azure credentials
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic(err)
	}

	// initialize Azure client
	azureClient, err := armcontainerinstance.NewClientFactory(os.Getenv("AZURE_SUBSCRIPTION_ID"), cred, nil)
	if err != nil {
		panic(err)
	}

	// define deployment request
	containerGroup := armcontainerinstance.ContainerGroup{
		Properties: &armcontainerinstance.ContainerGroupPropertiesProperties{
			Containers: []*armcontainerinstance.Container{
				{
					Name: to.Ptr(containerName),
					Properties: &armcontainerinstance.ContainerProperties{
						Command:              []*string{},
						EnvironmentVariables: []*armcontainerinstance.EnvironmentVariable{},
						Image:                to.Ptr(addr),
						Ports: []*armcontainerinstance.ContainerPort{
							{
								Port: to.Ptr[int32](3000),
							}},
						Resources: &armcontainerinstance.ResourceRequirements{
							Requests: &armcontainerinstance.ResourceRequests{
								CPU:        to.Ptr[float64](1),
								MemoryInGB: to.Ptr[float64](1.5),
							},
						},
					},
				}},
			IPAddress: &armcontainerinstance.IPAddress{
				Type: to.Ptr(armcontainerinstance.ContainerGroupIPAddressTypePublic),
				Ports: []*armcontainerinstance.Port{
					{
						Port:     to.Ptr[int32](3000),
						Protocol: to.Ptr(armcontainerinstance.ContainerGroupNetworkProtocolTCP),
					}},
			},
			OSType:        to.Ptr(armcontainerinstance.OperatingSystemTypesLinux),
			RestartPolicy: to.Ptr(armcontainerinstance.ContainerGroupRestartPolicyOnFailure),
		},
		Location: to.Ptr(containerGroupLocation),
	}

	poller, err := azureClient.NewContainerGroupsClient().BeginCreateOrUpdate(ctx, resourceGroupName, containerGroupName, containerGroup, nil)
	if err != nil {
		panic(err)
	}

	// send request and wait until done
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		panic(err)
	}

	// print ref
	fmt.Println("Deployment for image", addr, "now available at", res.ContainerGroup.Properties.IPAddress.IP)
}
