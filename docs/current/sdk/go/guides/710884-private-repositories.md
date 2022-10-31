---
slug: /sdk/go/710884/private-repositories
displayed_sidebar: 'current'
---

# Use Dagger with Private Git Repositories

Dagger uses your host's SSH authentication agent to securely authenticate against private remote Git repositories.

To clone private repositories, the only requirement is to run `ssh-add` on the Dagger host to add your SSH key to the authentication agent.

Assume that you have a Dagger CI tool containing the following code, which references a private repository:

```go file=../snippets/private-repositories/main.go
```

Now, first remove all the SSH keys from the authentication agent on the Dagger host:

```shell
➜  ssh-add -D
All identities removed.
```

Attempt to run the Go CI tool:

```shell
➜  go run .
panic: input:1: git.branch.tree.file.contents failed to load cache key: failed to fetch remote 
exit status 128
```

The CI tool fails, as it is unable to find the necessary authentication credentials to read the private repository in the SSH authentication agent.

Now, add the SSH key to the authentication agent on the host and try again:

```shell
➜ ssh-add
Identity added: xxxxx
go run .  
readme #
```

Finally, the CI tool succeeds in reading the private Git repository.
