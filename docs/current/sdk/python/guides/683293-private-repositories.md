---
slug: /683293/private-repositories
displayed_sidebar: "current"
---

# Use Dagger with Private Git Repositories

Dagger uses your host's SSH authentication agent to securely authenticate against private remote Git repositories.

To clone private repositories, the only requirement is to run `ssh-add` on the Dagger host to add your SSH key to the authentication agent.

Assume that you have a Dagger CI tool containing the following code, which references a private repository:

```python file=../snippets/private-repositories/clone.py
```

Now, first remove all the SSH keys from the authentication agent on the Dagger host:

```shell
➜  ssh-add -D
All identities removed.
```

Attempt to run the Go CI tool:

```shell
➜  python clone.py
{'message': 'failed to load cache key: failed to fetch remote https://xxxxx@private-repository.git: exit status 128', 'locations': [{'line': 6, 'column': 11}], 'path': ['git', 'branch', 'tree', 'file', 'contents']}
```

The CI tool fails, as it is unable to find the necessary authentication credentials to read the private repository in the SSH authentication agent.

Now, add the SSH key to the authentication agent on the host and try again:

```shell
➜ ssh-add
Identity added: xxxxx
➜ python clone.py
readme #
```

Finally, the CI tool succeeds in reading the private Git repository.
