# Example

:warning: This directory shall not be push to main, it's just an example for the PR.

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

One command is currently available:

```shell
$ dagger do
┃ Loading+installing project...                                                                                                              
┃ Usage:                                                                                                                                     
┃   dagger do [flags]                                                                                                                        
┃   dagger do [command]                                                                                                                      
┃                                                                                                                                            
┃ Available Commands:                                                                                                                        
┃   foo         Test doc  
```

```shell
dagger do foo --help
┣─╮                                                                                                                                          
│ ▼ host.directory /Users/tomchauveau/Documents/DAGGER/dagger/sdk/nodejs/tmp                                                                 
█ [43.7s] dagger do foo --name foo --age bar
┃ Loading+installing project...                                                                                                              
┃ Running command "foo"...                                                                                                                   
┃                                                                                                                                            
┃ Command foo - Test doc                                                                                                                     
┃                                                                                                                                            
┃ Available Subcommands:                                                                                                                     
┃   --age   test                                                                                                                             
┃   --name  test  
```

## Notes

- I'm not sure we will be able to support entrypoint on javascript since there's no way to introspect function arguments types, so we cannot create a typed GraphQL schema. Maybe primitive types can be used but it is not supported by this current version. 

## To do

- [x] Handle command execution
- [ ] Make errors more understandable
- [ ] Improve generator system to use a public library
- [x] Find a way to import local library for integration tests
- [ ] Add tests
