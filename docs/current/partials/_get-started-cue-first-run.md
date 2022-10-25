Ensure that Docker is running, then download the example application and run its CI/CD pipeline locally:

```shell
git clone https://github.com/dagger/todoapp
cd todoapp
dagger-cue project update
dagger-cue do build
```
