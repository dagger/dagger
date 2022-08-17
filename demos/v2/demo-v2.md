# Project Cloak demo v2

I want to improve the automation on my project: todoapp.

Why?

* See all my workflows in one place
* Auto*caching makes everything faster
* Same behavior on everyone's machines
* Reuse workflows between dev and CI
* I can run expensive workflows on remote workers

## 1. Checkout my project repository

```bash
git clone ssh://git@github.com/dagger/todoapp
```

## 2. Write my first workflow: build

    [SCENARIO: typescript with raw gql]

* Create project file `dagger.yaml`:
  
            ```yaml
            workflows:
                build:
                    source: ./workflows
                    sdk: typescript
            ```

  [P2: sdk is implicit via auto-discovery]

* Create workflow directory `workflows`

* Write workflow implementation `workflows/index.ts`
  
    ```typescript
    ??? FIXME
    ```

* Write extra files for SDK
  * package.json [P2: make package.json optional]
  * tsconfig.json [P2: make tsconfig.json optional]

* Run `dagger do` : shows new workflow in help message
  
* Run `dagger do build`: it works! Running my workflow
  
    [P2 bundle buildkit so I don't have to run it separately]

* Run `dagger do build` again: it's super fast because of caching

## 3. Simplify my workflow by using an EXTENSION

  * Add yarn extension in my workflow dependencies `dagger.yaml`
    [P1 dependencies can be loaded from "fake universe", actually a configurable local directory]
    
  * Craft new, simpler queries in interactive sandbox
  * Simplify `index.ts`
  * Run `dagger do`: it works again!

## 4. Write my second workflow: deploy

  * Add `deploy` workflow in `dagger.yaml`
  * Write workflow implementation in `workflows/index.ts`
    * Craft new queries in sandbox (show that netlify is there)
    * [P1 worfklow can access project dir]
    * [P1 workflow can access environment variable]
  * Run `dagger do deploy`
  * Run again with extra parameters
    * [P2: support passing parameters to workflow]
    * [P1: consensus on how paramters will be passed to workflows in the future]
  

## 5. Write my own extension: vercel!

[P2: implement a working vercel extension]