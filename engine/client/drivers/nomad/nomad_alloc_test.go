package nomadalloc

import (
	"errors"
	"net/url"
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
