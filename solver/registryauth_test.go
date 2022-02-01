package solver

import (
	"testing"
)

func TestParseAuthHost(t *testing.T) {
	type hcase struct {
		Host, Domain string
	}

	scases := []hcase{
		// Short
		{
			Host:   "foo",
			Domain: "docker.io",
		},
		{
			Host:   "foo:1.1",
			Domain: "docker.io",
		},
		{
			Host:   "foo@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "docker.io",
		},

		// Short image
		{
			Host:   "foo/bar",
			Domain: "docker.io",
		},
		{
			Host:   "foo/bar:1.1",
			Domain: "docker.io",
		},
		{
			Host:   "foo/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "docker.io",
		},

		// Private registry
		{
			Host:   "registry.com",
			Domain: "registry.com",
		},
		{
			Host:   "registry.com:1.1",
			Domain: "registry.com",
		},
		{
			Host:   "registry.com@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "registry.com",
		},

		// Private image
		{
			Host:   "registry.com/foo/bar",
			Domain: "registry.com",
		},
		{
			Host:   "registry.com/foo/bar:1.1",
			Domain: "registry.com",
		},
		{
			Host:   "registry.com/foo/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "registry.com",
		},

		// Private registry with port
		{
			Host:   "registry.com:5000",
			Domain: "registry.com:5000",
		},
		{
			Host:   "registry.com:5000:1.1",
			Domain: "registry.com:5000",
		},
		{
			Host:   "registry.com:5000@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "registry.com:5000",
		},

		// Private image with port
		{
			Host:   "registry.com:5000/foo/bar",
			Domain: "registry.com:5000",
		},
		{
			Host:   "registry.com:5000/foo/bar:1.1",
			Domain: "registry.com:5000",
		},
		{
			Host:   "registry.com:5000/foo/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "registry.com:5000",
		},

		// docker.io short
		{
			Host:   "docker.io",
			Domain: "docker.io",
		},
		{
			Host:   "docker.io:1.1",
			Domain: "docker.io",
		},
		{
			Host:   "docker.io@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "docker.io",
		},

		// docker.io image
		{
			Host:   "docker.io/foo/bar",
			Domain: "docker.io",
		},
		{
			Host:   "docker.io/foo/bar:1.1",
			Domain: "docker.io",
		},
		{
			Host:   "docker.io/foo/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "docker.io",
		},

		// registry-1.docker.io short
		{
			Host:   "registry-1.docker.io",
			Domain: "docker.io",
		},
		{
			Host:   "registry-1.docker.io:1.1",
			Domain: "docker.io",
		},
		{
			Host:   "registry-1.docker.io@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "docker.io",
		},

		// registry-1.docker.io image
		{
			Host:   "registry-1.docker.io/foo/bar",
			Domain: "docker.io",
		},
		{
			Host:   "registry-1.docker.io/foo/bar:1.1",
			Domain: "docker.io",
		},
		{
			Host:   "registry-1.docker.io/foo/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "docker.io",
		},

		// index.docker.io short
		{
			Host:   "index.docker.io",
			Domain: "docker.io",
		},
		{
			Host:   "index.docker.io:1.1",
			Domain: "docker.io",
		},
		{
			Host:   "index.docker.io@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "docker.io",
		},

		// index.docker.io image
		{
			Host:   "index.docker.io/foo/bar",
			Domain: "docker.io",
		},
		{
			Host:   "index.docker.io/foo/bar:1.1",
			Domain: "docker.io",
		},
		{
			Host:   "index.docker.io/foo/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "docker.io",
		},

		// localhost repository
		{
			Host:   "localhost",
			Domain: "localhost",
		},
		{
			Host:   "localhost:1.1",
			Domain: "localhost",
		},
		{
			Host:   "localhost@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "localhost",
		},

		// localhost image
		{
			Host:   "localhost/foo/bar",
			Domain: "localhost",
		},
		{
			Host:   "localhost/foo/bar:1.1",
			Domain: "localhost",
		},
		{
			Host:   "localhost/foo/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "localhost",
		},

		// localhost repository with port
		{
			Host:   "localhost:5000",
			Domain: "localhost:5000",
		},
		{
			Host:   "localhost:5000:1.1",
			Domain: "localhost:5000",
		},
		{
			Host:   "localhost:5000@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "localhost:5000",
		},

		// localhost image with port
		{
			Host:   "localhost:5000/foo/bar",
			Domain: "localhost:5000",
		},
		{
			Host:   "localhost:5000/foo/bar:1.1",
			Domain: "localhost:5000",
		},
		{
			Host:   "localhost:5000/foo/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb",
			Domain: "localhost:5000",
		},

		// empty host
		{
			Host:   "",
			Domain: "docker.io",
		},
		{
			Host:   "/jo",
			Domain: "docker.io",
		},
	}

	fcases := []hcase{
		{
			Host: ":/jo",
		},
	}

	type output struct {
		expected, actual string
	}

	successRefs := []output{}
	for _, scase := range scases {
		named, err := ParseAuthHost(scase.Host)
		if err != nil {
			t.Fatalf("Invalid normalized reference for [%q]. Got %q", scase, err)
		}
		successRefs = append(successRefs, output{
			actual:   named,
			expected: scase.Domain,
		})
	}
	for _, r := range successRefs {
		if r.expected != r.actual {
			t.Fatalf("Invalid normalized reference for [%q]. Expected %q, got %q", r, r.expected, r.actual)
		}
	}

	for _, fcase := range fcases {
		named, err := ParseAuthHost(fcase.Host)
		if err == nil {
			t.Fatalf("Invalid normalized reference for [%q]. Expected failure for %q", fcase, named)
		}
	}
}
