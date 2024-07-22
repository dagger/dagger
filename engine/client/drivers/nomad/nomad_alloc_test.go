package nomadalloc

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpecFromURL(t *testing.T) {
	// table test for SpecFromURL input is a string and expected output is a Spec struct

	tests := []struct {
		name     string
		input    string
		expected Spec
	}{
		{
			name:  "simple",
			input: "nomad-alloc://alloc123",
			expected: Spec{
				Alloc: "alloc123",
			},
		},
		{
			name:  "without-alloc",
			input: "nomad-alloc://",
			expected: Spec{
				Alloc: "",
			},
		},

		{
			name:  "with-namespace",
			input: "nomad-alloc://alloc123?namespace=ns1",
			expected: Spec{
				Alloc:     "alloc123",
				Namespace: "ns1",
			},
		},
		{
			name:  "with-region",
			input: "nomad-alloc://alloc123?region=reg1",
			expected: Spec{
				Alloc:  "alloc123",
				Region: "reg1",
			},
		},
		{
			name:  "with-job",
			input: "nomad-alloc://?job=job1",
			expected: Spec{

				Job: "job1",
			},
		},
		{
			name:  "with-task",
			input: "nomad-alloc://alloc123?task=task1",
			expected: Spec{
				Alloc: "alloc123",
				Task:  "task1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.input)

			if err != nil {
				t.Fatal(err)
			}
			spec, err := SpecFromURL(u)

			if err != nil {
				t.Fatal(err)
			}
			require.Equal(t, tt.expected, *spec)
		})
	}

}
func TestSpecFromURLErrors(t *testing.T) {

	tt := []struct {
		name     string
		input    string
		expected error
	}{
		{
			name:     "alloc-missing",
			input:    "nomad-alloc://?namespace=ns1",
			expected: errors.New("url should have either alloc or job"),
		},
		{
			name:     "alloc-and-job",
			input:    "nomad-alloc://",
			expected: errors.New("url should not have both alloc and job"),
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			_, err = SpecFromURL(u)
			require.Equal(t, tc.expected, err)
		})
	}
}
func TestHelper(t *testing.T) {

	os.Setenv("NOMAD_ADDR", "http://localhost:4646")

	nomadAddr := os.Getenv("NOMAD_ADDR")
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	if nomadAddr == "" {
		t.Skip("NOMAD_ADDR not set")
	}
	// cleanup := launchNomadJob(t)
	// defer cleanup()

	u, err := url.Parse("nomad-alloc://testengine")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := Helper(u)
	if err != nil {
		t.Fatal(err)
	}
	if ch == nil {
		t.Fatal("expected con to be not nil")
	}
	con, err := ch.ContextDialer(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if con == nil {
		t.Fatal("expected con to be not nil")
	}

}

func launchNomadJob(t *testing.T) func() {
	t.Helper()

	job := `
	job "testengine" {
		datacenters = ["*"]
		
		group "group1" {
			count = 1
			task "task1" {
				driver = "docker"
				config {
					image = "registry.dagger.io/engine:v0.12.0"

					cap_add = ["sys_admin"]
					privileged = true
					
					volumes = ["alloc/data/runner:/var/lib/dagger"]
				}
				
			}
		}
	}
	`
	// exec nomad run job - <<EOF
	// $job
	// EOF

	err := exec.Command("nomad", "run", "-", "<<EOF", job, "EOF").Run()

	if err != nil {
		t.Fatal(err)
	}

	return func() {

		exec.Command("nomad", "stop", "testengine", "-purge").Run()
	}

	// launch a nomad job
}
