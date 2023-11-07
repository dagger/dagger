---
slug: /018362/gpu-access
---

# GPU access with Dagger

## Introduction

This tutorial guides you through the newly introduced GPU access feature. [Paperspace](https://www.paperspace.com/) will be the cloud platform used to run the pipelines described here.

## Requirements

- You have a basic understanding of Go and Python.
- You have a basic understanding of SSH and remote shells.
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/) - stpe 2 covers this part.
- You have [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html) installed on th host - step 2 covers this part.
- You have a cloud provider that supports Nvidia GPUs - [Paperspace](https://www.paperspace.com) will be used in this guide.

## Step 1: Configuring and provisioning the instance

After signing up to [Paperspace](https://www.paperspace.com) open the web console and click on the top right user icon, then click on "Your account" and switch to the "SSH Keys" tab. Add an SSH key from your local host, this will be used to access the remote instance after provisioning is done.

After an SSH key is properly set, click on the top left Paperspace logo and pick "Core - Virtual Servers".

Click on "Create a machine" and pick "Ubuntu 22.04". In the "Machine" section pick "P4000". This will provision an instance with a single GPU: Nvidia Quadro P4000. Feel free to pick a region that's more convenient to you and click "Create machine".

Paperspace will take some time to provision the instance and once this is done you should be able to see it on the "Machines" tab in the main console view.

## Step 2: Requirements setup

Once the machine is created and visible on the main console view, click "Start" if it's not currently up. Once is up you should see a "Connect" button instead. Click "Connect" and copy the SSH command from it. It will look as follows:

```shell
ssh paperspace@xxx.xxx.xxx.xx
```

First we'll install and try out [Docker](https://docker.io):

```shell
# Add Docker's official GPG key:
sudo apt-get update
sudo apt-get install ca-certificates curl gnupg
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg
# Add the repository to Apt sources:
echo \
  "deb [arch="$(dpkg --print-architecture)" signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \
  "$(. /etc/os-release && echo "$VERSION_CODENAME")" stable" | \
  sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo usermod -aG docker $USER
newgrp docker
docker run hello-world
```

Then continue with [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html).

Add Nvidia's repositories to the system:
```shell
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg \
  && curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
    sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
    sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list \
  && \
    sudo apt-get update
```

Install the NVIDIA Container Toolkit package:

```shell
sudo apt-get install -y nvidia-container-toolkit
```

And ensure the latest driver is installed:

```shell
sudo apt-get install -y nvidia-driver-535
```

Run a post setup command:

```shell
sudo nvidia-ctk runtime configure --runtime=docker
```

And reboot the instance:

```shell
sudo reboot
```

Once the instance is up and running again run the following command to verify that GPU visibility is working properly - you should expect a list of available GPUs, on a multi GPU system there would be multiple lines-:

```shell
nvidia-smi -L
```

## Step 3: Setup Dagger with GPU support

Install the Dagger CLI by running:

```shell
cd /usr/local
curl -L https://dl.dagger.io/dagger/install.sh | DAGGER_VERSION=0.9.3 sh
```

Note that we use `0.9.3` instead of the version that's mentioned in the [official quickstart](https://docs.dagger.io/quickstart/729236/cli/#install-the-dagger-cli), the reason is that GPU support became available in that particular release.

Now that Dagger CLI is setup ensure that experimental GPU support is enabled by setting the following environment variable:

```shell
export _EXPERIMENTAL_DAGGER_GPU_SUPPORT=1
```

After this you should be ready to write a GPU enabled Dagger pipeline!

## Step 4: Create a Dagger pipeline

Create a new directory for the Golang project, initialize the main module and install the Dagger SDK:

```shell
mkdir ~/pipeline
cd ~/pipeline

# Initialize the project and install the latest Dagger SDK:
go mod init main
go get dagger.io/dagger@latest
```

Create a `main.go` file and add the following contents:

```go
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

        // Use a CUDA container image for this pipeline
        ctr := client.Container().From("nvidia/cuda:11.7.1-base-ubuntu20.04")

        // Run nvidia-smi to retrieve the list of GPUs available to the Dagger container:
        out, err := ctr.WithExec([]string{"nvidia-smi", "-L"}).Stdout(ctx)
        if err != nil {
                panic(err)
        }


        fmt.Println("available GPUs", out)
}
```

To run the pipeline use:

```shell
dagger run go run main.go
```

If everything is setup properly you should see the log line "available GPUs" with the specs of your GPU.

## Step 5: Extend the pipeline with PyTorch

Create a `main.py` with the following contents (this is a condensed version of the neural network tutorial in the official PyTorch documentation, for more details click [here](https://pytorch.org/tutorials/beginner/basics/quickstart_tutorial.html)):

```python
import torch
from torch import nn
from torch.utils.data import DataLoader
from torchvision import datasets
from torchvision.transforms import ToTensor

# Download training data from open datasets.
training_data = datasets.FashionMNIST(
    root="data",
    train=True,
    download=True,
    transform=ToTensor(),
)

# Download test data from open datasets.
test_data = datasets.FashionMNIST(
    root="data",
    train=False,
    download=True,
    transform=ToTensor(),
)

batch_size = 64

# Create data loaders.
train_dataloader = DataLoader(training_data, batch_size=batch_size)
test_dataloader = DataLoader(test_data, batch_size=batch_size)

# Get cpu, gpu or mps device for training.
device = (
    "cuda"
    if torch.cuda.is_available()
    else "mps"
    if torch.backends.mps.is_available()
    else "cpu"
)
print(f"Using {device} device")

# Define model
class NeuralNetwork(nn.Module):
    def __init__(self):
        super().__init__()
        self.flatten = nn.Flatten()
        self.linear_relu_stack = nn.Sequential(
            nn.Linear(28*28, 512),
            nn.ReLU(),
            nn.Linear(512, 512),
            nn.ReLU(),
            nn.Linear(512, 10)
        )

    def forward(self, x):
        x = self.flatten(x)
        logits = self.linear_relu_stack(x)
        return logits

model = NeuralNetwork().to(device)
print(model)

loss_fn = nn.CrossEntropyLoss()
optimizer = torch.optim.SGD(model.parameters(), lr=1e-3)

def train(dataloader, model, loss_fn, optimizer):
    size = len(dataloader.dataset)
    model.train()
    for batch, (X, y) in enumerate(dataloader):
        X, y = X.to(device), y.to(device)

        # Compute prediction error
        pred = model(X)
        loss = loss_fn(pred, y)

        # Backpropagation
        loss.backward()
        optimizer.step()
        optimizer.zero_grad()

        if batch % 100 == 0:
            loss, current = loss.item(), (batch + 1) * len(X)
            print(f"loss: {loss:>7f}  [{current:>5d}/{size:>5d}]")



def test(dataloader, model, loss_fn):
    size = len(dataloader.dataset)
    num_batches = len(dataloader)
    model.eval()
    test_loss, correct = 0, 0
    with torch.no_grad():
        for X, y in dataloader:
            X, y = X.to(device), y.to(device)
            pred = model(X)
            test_loss += loss_fn(pred, y).item()
            correct += (pred.argmax(1) == y).type(torch.float).sum().item()
    test_loss /= num_batches
    correct /= size
    print(f"Test Error: \n Accuracy: {(100*correct):>0.1f}%, Avg loss: {test_loss:>8f} \n")

epochs = 1
for t in range(epochs):
    print(f"Epoch {t+1}\n-------------------------------")
    train(train_dataloader, model, loss_fn, optimizer)
    test(test_dataloader, model, loss_fn)
print("Done!")
```

Now modify the original Go pipeline so that it does the following:

- Use an official PyTorch container image: https://hub.docker.com/r/pytorch/pytorch
- Mounts the current source directory into the container so that `main.py` can be invoked from the pipeline.

The pipeline should look as:

```go
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

	// Setup a source directory to be used inside the Dagger container
	// Omit the "main" binary that's created when building this sample:
	source := client.Host().Directory(".", dagger.HostDirectoryOpts{})

	// Use an official pytorch container image ad mount the above source directory as "/src":
	ctr := client.Container().From("pytorch/pytorch:latest").
		WithDirectory("/src", source).WithWorkdir("/src")

	// Run main.py:
	contents, err := ctr.WithExec([]string{"python3", "main.py"}).Stdout(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(contents)
}
```

To run the pipeline use:

```shell
dagger run go run main.go
```

The output will contain insights on the training process. As the original tutorial mentions you could increase the number of iterations (epochs) to observe accuracy increase.