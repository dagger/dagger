Dagger uses your host's SSH authentication agent to securely authenticate against private remote Git repositories.

To clone private repositories, the only requirement is to run `ssh-add` on the Dagger host to add your SSH key to the authentication agent.

Assume that you have a Dagger CI tool containing the following code, which references a private repository:
