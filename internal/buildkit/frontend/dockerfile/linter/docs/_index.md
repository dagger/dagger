---
title: Build checks
description: |
  BuildKit has built-in support for analyzing your build configuration based on
  a set of pre-defined rules for enforcing Dockerfile and building best
  practices.
keywords: buildkit, linting, dockerfile, frontend, rules
---

BuildKit has built-in support for analyzing your build configuration based on a
set of pre-defined rules for enforcing Dockerfile and building best practices.
Adhering to these rules helps avoid errors and ensures good readability of your
Dockerfile.

Checks run as a build invocation, but instead of producing a build output, it
performs a series of checks to validate that your build doesn't violate any of
the rules. To run a check, use the `--check` flag:

```console
$ docker build --check .
```

To learn more about how to use build checks, see
[Checking your build configuration](https://docs.docker.com/build/checks/).

<table>
  <thead>
    <tr>
      <th>Name</th>
      <th>Description</th>
    </tr>
  </thead>
  <tbody>
    {{- range .Rules }}
    <tr>
      <td><a href="./{{ .PageName }}/">{{ .Name }}{{- if .Experimental }} (experimental){{- end}}</a></td>
      <td>{{ .Description }}</td>
    </tr>
    {{- end }}
  </tbody>
</table>
