package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

const (
	// imageName defines the name of Nvidia's CUDA contaimer image:
	imageName = "nvidia/cuda"
	// imageFlavor sets the default image flavor, as defined in: https://hub.docker.com/r/nvidia/cuda
	imageFlavor = "base"

	// torchTensorsSample is a PyTorch sample taken from the official docs:
	// https://pytorch.org/tutorials/beginner/pytorch_with_examples.html#pytorch-tensors
	torchTensorsSample = `# -*- coding: utf-8 -*-
import torch
import math
dtype = torch.float
device = torch.device("cuda:0")
# Create random input and output data
x = torch.linspace(-math.pi, math.pi, 2000, device=device, dtype=dtype)
y = torch.sin(x)
# Randomly initialize weights
a = torch.randn((), device=device, dtype=dtype)
b = torch.randn((), device=device, dtype=dtype)
c = torch.randn((), device=device, dtype=dtype)
d = torch.randn((), device=device, dtype=dtype)
learning_rate = 1e-6
for t in range(2000):
    # Forward pass: compute predicted y
    y_pred = a + b * x + c * x ** 2 + d * x ** 3
    # Compute and print loss
    loss = (y_pred - y).pow(2).sum().item()
    if t % 100 == 99:
        print(t, loss)
    # Backprop to compute gradients of a, b, c, d with respect to loss
    grad_y_pred = 2.0 * (y_pred - y)
    grad_a = grad_y_pred.sum()
    grad_b = (grad_y_pred * x).sum()
    grad_c = (grad_y_pred * x ** 2).sum()
    grad_d = (grad_y_pred * x ** 3).sum()
    # Update weights using gradient descent
    a -= learning_rate * grad_a
    b -= learning_rate * grad_b
    c -= learning_rate * grad_c
    d -= learning_rate * grad_d
print(f'Result: y = {a.item()} + {b.item()} x + {c.item()} x^2 + {d.item()} x^3')
	`
	gpuTestsEnabledEnvName = "DAGGER_GPU_TESTS_ENABLED"
)

var (
	// cudaImageMatrix establishes an image name matrix so that tests can
	// run in a combination of versions and distro flavors:
	cudaImageMatrix = []string{
		cudaImageName("11.7.1", "ubuntu20.04"),
		cudaImageName("11.7.1", "ubi8"),
		cudaImageName("11.7.1", "centos7"),
	}
)

// cudaImageName is a helper that returns the CUDA image name
// cudaImageName("11.7.1", "centos7") results in "nvidia/cuda:11.7.1-base-centos7":
func cudaImageName(version string, distroFlavor string) string {
	imageName := fmt.Sprintf(
		"%s:%s-%s-%s",
		imageName,
		version,
		imageFlavor,
		distroFlavor,
	)
	return imageName
}

var (
	// uuidRegex is used to match GPU UUID in nvidia-smi output:
	uuidRegex = regexp.MustCompile(`GPU-\w{8}-\w{4}-\w{4}-\w{4}-\w{12}`)
)

func TestGPUAccess(t *testing.T) {
	if gpuTestsEnabled := os.Getenv(gpuTestsEnabledEnvName); gpuTestsEnabled == "" {
		t.Skip("Skipping GPU Tests")
	}
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	// Query nvidia-smi on the host:
	hostNvidiaCmd := exec.Command("nvidia-smi", "-L")
	hostNvidiaOutput, err := hostNvidiaCmd.Output()
	hostNvidiaOutputStr := string(hostNvidiaOutput)
	require.NoError(t, err)
	require.NotEmpty(t, hostNvidiaOutput)

	// Iterate through the image matrix:
	for _, cudaImage := range cudaImageMatrix {
		t.Run(cudaImage, func(t *testing.T) {
			// Query the same on the Dagger container and compare output:
			ctr := c.Container().From(cudaImage)
			contents, err := ctr.
				// WithGPU(dagger.ContainerWithGPUOpts{Devices: "GPU-5d8950fe-17a6-2fa7-9baa-afa83bba0e2b"}).
				ExperimentalWithAllGPUs().
				WithExec([]string{"nvidia-smi", "-L"}).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, hostNvidiaOutputStr, contents)

			t.Run("use specific GPU", func(t *testing.T) {
				var gpus []string

				// Take host output and get GPU IDs:
				for _, ln := range strings.Split(hostNvidiaOutputStr, "\n") {
					matches := uuidRegex.FindAllString(ln, 1)
					if len(matches) == 0 {
						continue
					}
					gpus = append(gpus, matches[0])
				}

				if len(gpus) <= 1 {
					t.Skip("skipping - this test requires at least 2 GPUs to run")
				}

				// Pick first GPU and initialize a Dagger container for it:
				ctr := c.Container().From(cudaImage)
				contents, err := ctr.
					ExperimentalWithGPU([]string{gpus[0]}).
					WithExec([]string{"nvidia-smi", "-L"}).
					Stdout(ctx)
				require.NoError(t, err)

				// Assert that only the first GPU is present in the output
				// and ensure none of the other GPUs are:
				require.Contains(t, contents, gpus[0])
				for _, gpu := range gpus[1:] {
					require.NotContains(t, contents, gpu)
				}
			})
		})
	}
}

func TestGPUAccessWithPython(t *testing.T) {
	if gpuTestsEnabled := os.Getenv(gpuTestsEnabledEnvName); gpuTestsEnabled == "" {
		t.Skip("Skipping GPU Tests")
	}
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	t.Run("pytorch CUDA availability check", func(t *testing.T) {
		ctr := c.Container().From("pytorch/pytorch:latest")
		contents, err := ctr.
			ExperimentalWithAllGPUs().
			WithExec([]string{"python3", "-c", "import torch; print(torch.cuda.is_available())"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, contents, "True")
	})

	t.Run("pytorch tensors sample", func(t *testing.T) {
		ctr := c.Container().From("pytorch/pytorch:latest")
		contents, err := ctr.
			ExperimentalWithAllGPUs().
			WithNewFile("/tmp/tensors.py", dagger.ContainerWithNewFileOpts{
				Contents: torchTensorsSample,
			}).
			WithExec([]string{"python3", "/tmp/tensors.py"}).
			Stdout(ctx)
		require.NoError(t, err)

		// If CUDA fails to load or the computation fails the results line isn't printed:
		require.Contains(t, contents, "Result")
	})
}
