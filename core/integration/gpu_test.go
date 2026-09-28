package core

// These tests cover GPU device access through container execution.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

type GPUSuite struct{}

func TestGPU(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(GPUSuite{})
}

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

// cudaImageMatrix establishes an image name matrix so that tests can
// run in a combination of versions and distro flavors:
var cudaImageMatrix = []string{
	cudaImageName("11.7.1", "ubuntu20.04"),
	cudaImageName("11.7.1", "ubi8"),
	cudaImageName("11.7.1", "centos7"),
}

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

// uuidRegex is used to match GPU UUID in nvidia-smi output:
var uuidRegex = regexp.MustCompile(`GPU-\w{8}-\w{4}-\w{4}-\w{4}-\w{12}`)

func (GPUSuite) TestGPUAccess(ctx context.Context, t *testctx.T) {
	if gpuTestsEnabled := os.Getenv(gpuTestsEnabledEnvName); gpuTestsEnabled == "" {
		t.Skip("Skipping GPU Tests")
	}
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
		t.Run(cudaImage, func(ctx context.Context, t *testctx.T) {
			// Query the same on the Dagger container and compare output:
			ctr := c.Container().From(cudaImage)
			contents, err := ctr.
				WithGPU().
				WithExec([]string{"nvidia-smi", "-L"}).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, hostNvidiaOutputStr, contents)

			t.Run("deprecated experimentalWithAllGPUs alias", func(ctx context.Context, t *testctx.T) {
				//nolint:staticcheck // deprecated alias kept for compatibility
				contents, err := c.Container().From(cudaImage).
					ExperimentalWithAllGPUs().
					WithExec([]string{"nvidia-smi", "-L"}).
					Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, hostNvidiaOutputStr, contents)
			})

			t.Run("use specific GPU", func(ctx context.Context, t *testctx.T) {
				var gpus []string

				// Take host output and get GPU IDs:
				for ln := range strings.SplitSeq(hostNvidiaOutputStr, "\n") {
					matches := uuidRegex.FindAllString(ln, 1)
					if len(matches) == 0 {
						continue
					}
					gpus = append(gpus, matches[0])
				}

				if len(gpus) <= 1 {
					t.Skip("skipping - this test requires at least 2 GPUs to run")
				}

				// Pick first GPU and initialize a Dagger container for it.
				// Device selection has no non-deprecated replacement yet.
				ctr := c.Container().From(cudaImage)
				//nolint:staticcheck // deprecated alias kept for compatibility
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

func (GPUSuite) TestGPUAccessWithPython(ctx context.Context, t *testctx.T) {
	if gpuTestsEnabled := os.Getenv(gpuTestsEnabledEnvName); gpuTestsEnabled == "" {
		t.Skip("Skipping GPU Tests")
	}
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	t.Run("pytorch CUDA availability check", func(ctx context.Context, t *testctx.T) {
		ctr := c.Container().From("pytorch/pytorch:latest")
		contents, err := ctr.
			WithGPU().
			WithExec([]string{"python3", "-c", "import torch; print(torch.cuda.is_available())"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, contents, "True")
	})

	t.Run("pytorch tensors sample", func(ctx context.Context, t *testctx.T) {
		ctr := c.Container().From("pytorch/pytorch:latest")
		contents, err := ctr.
			WithGPU().
			WithNewFile("/tmp/tensors.py", torchTensorsSample).
			WithExec([]string{"python3", "/tmp/tensors.py"}).
			Stdout(ctx)
		require.NoError(t, err)

		// If CUDA fails to load or the computation fails the results line isn't printed:
		require.Contains(t, contents, "Result")
	})
}

// The tests below need no GPU. They cover everything the engine controls:
// the support gate, the OCI hook injection, and the device list handed to
// the NVIDIA toolkit. Real device passthrough is only covered above.

// An engine without GPU support must refuse a GPU exec up front.
func (GPUSuite) TestWithGPURequiresEngineSupport(ctx context.Context, t *testctx.T) {
	if os.Getenv(gpuTestsEnabledEnvName) != "" {
		t.Skip("engine has GPU support enabled")
	}
	c := connect(ctx, t)

	_, err := c.Container().From(alpineImage).
		WithGPU().
		WithExec([]string{"true"}).
		Sync(ctx)
	requireErrOut(t, err, "GPU support is not enabled")
}

// gpuStubHookPath is where the executor expects the NVIDIA runtime hook. OCI
// prestart hooks run inside the engine container, so a stub there is enough
// to exercise the whole exec path on a host with no GPU.
const gpuStubHookPath = "/usr/bin/nvidia-container-runtime-hook"

func gpuDevEngine(c *dagger.Client, stubHook bool) *dagger.Service {
	return devEngineContainerAsService(devEngineContainer(c, func(ctr *dagger.Container) *dagger.Container {
		ctr = ctr.WithEnvVariable("_EXPERIMENTAL_DAGGER_GPU_SUPPORT", "true")
		if stubHook {
			// A script, not a copy of /usr/bin/true: the engine image's
			// coreutils is a multi-call binary that dispatches on argv[0].
			ctr = ctr.WithNewFile(gpuStubHookPath, "#!/bin/sh\nexit 0\n", dagger.ContainerWithNewFileOpts{Permissions: 0o755})
		}
		return ctr
	}))
}

func gpuQuery(field string) string {
	return fmt.Sprintf(`{ container { from(address: %q) { %s { withExec(args: ["sh", "-c", "echo $NVIDIA_VISIBLE_DEVICES"]) { stdout } } } } }`, alpineImage, field)
}

// With GPU support enabled and the hook stubbed, every spelling of the API
// lands in NVIDIA_VISIBLE_DEVICES of the executed process.
func (GPUSuite) TestWithGPUDeviceEnv(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	clientCtr := engineClientContainer(ctx, t, c, gpuDevEngine(c, true))

	for _, tc := range []struct {
		name  string
		field string
		want  string
	}{
		{name: "withGPU", field: "withGPU", want: "all"},
		{name: "deprecated experimentalWithAllGPUs", field: "experimentalWithAllGPUs", want: "all"},
		{name: "deprecated experimentalWithGPU", field: `experimentalWithGPU(devices: ["GPU-aaaa", "GPU-bbbb"])`, want: "GPU-aaaa,GPU-bbbb"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			out, err := clientCtr.
				WithNewFile("/query.graphql", gpuQuery(tc.field)).
				WithExec([]string{"dagger", "query", "--doc", "/query.graphql"}, dagger.ContainerWithExecOpts{DisableDaggerInDagger: true}).
				Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, fmt.Sprintf("%q", tc.want+"\n"))
		})
	}
}

// Without the hook binary the runtime cannot start the container, which
// proves the executor injected the hook and not only the env var.
func (GPUSuite) TestWithGPUInjectsHook(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	clientCtr := engineClientContainer(ctx, t, c, gpuDevEngine(c, false))

	_, err := clientCtr.
		WithNewFile("/query.graphql", gpuQuery("withGPU")).
		WithExec([]string{"dagger", "query", "--doc", "/query.graphql"}, dagger.ContainerWithExecOpts{DisableDaggerInDagger: true}).
		Sync(ctx)
	requireErrOut(t, err, "nvidia-container-runtime-hook")
}
