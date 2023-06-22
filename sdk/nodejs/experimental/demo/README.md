## Usage

Since we cannot install our local dagger library into the container that executes the command, for testing purpose we are copying local `nodejs` sdk by adding local `@dagger.io/dagger` into package dependencies.

For example `npm install ./node.js`

:bulb: Note that final users will simply import the function from `@dagger.io/dagger`.

To enable dagger project, you need to create a `dagger.json` file at the root of the project.

Here's an example

```json
{
  "name": "typescript test",
  "sdk": "typescript"
}
```

**Available command**

```shell
dagger do

 Available Commands:                                                                        
┃   build       Build the go binary from the given repo, branch and subpath.                 
┃   test        Test the go binary from the given repo, branch and subpath.
```

**Example**

```shell
dagger do -p experimental/demo build --repo ""https://github.com/dagger/dagger"" --subpath "./cmd/dagger"
```
