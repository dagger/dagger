// Go version
variable "GO_VERSION" {
  default = "1.16"
}

target "go-version" {
  args = {
    GO_VERSION = GO_VERSION
  }
}

// GitHub reference as defined in GitHub Actions (eg. refs/head/master)
variable "GITHUB_REF" {
  default = ""
}

target "git-ref" {
  args = {
    GIT_REF = GITHUB_REF
  }
}

// Special target: https://github.com/crazy-max/ghaction-docker-meta#bake-definition
target "ghaction-docker-meta" {
  tags = ["dagger:local"]
}

group "default" {
  targets = ["image-local"]
}

group "validate" {
  targets = ["vendor-validate", "golangci-lint", "cue-fmt"]
}

target "vendor-validate" {
  inherits = ["go-version"]
  target = "vendor-validate"
}

target "vendor-update" {
  inherits = ["go-version"]
  target = "vendor-update"
  output = ["."]
}

target "golangci-lint" {
  inherits = ["go-version"]
  target = "golangci-lint"
}

target "cue-fmt" {
  inherits = ["go-version"]
  target = "cue-fmt"
}

target "test" {
  inherits = ["go-version"]
  target = "test-coverage"
  output = ["."]
}

target "artifact" {
  inherits = ["go-version", "git-ref"]
  target = "artifact"
  output = ["./dist"]
}

target "artifact-all" {
  inherits = ["artifact"]
  platforms = [
    "linux/amd64",
    "linux/arm/v6",
    "linux/arm/v7",
    "linux/arm64",
    "linux/ppc64le",
    "windows/amd64",
    "darwin/arm64",
    "darwin/amd64"
  ]
}

target "image-e2e" {
  inherits = ["go-version"]
  tags = ["dagger-e2e:local"]
  output = ["type=docker"]
  target = "e2e"
}

target "image" {
  inherits = ["go-version", "git-ref", "ghaction-docker-meta"]
}

target "image-local" {
  inherits = ["image"]
  output = ["type=docker"]
}

target "image-all" {
  inherits = ["image"]
  platforms = [
    "linux/amd64",
    "linux/arm/v6",
    "linux/arm/v7",
    "linux/arm64",
    "linux/ppc64le"
  ]
}
