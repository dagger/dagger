Dagger recommends you to rely on your host's SSH authentication agent to securely authenticate against private remote Git repositories.

To clone private repositories, the only requirements are to run `ssh-add` on the Dagger host (to add your SSH key to the authentication agent), and mount its socket using the `SSHAuthSocket` parameter of the `(Dagger.GitRef).Tree` API.

Assume that you have a Dagger CI tool containing the following code, which references a private repository:
