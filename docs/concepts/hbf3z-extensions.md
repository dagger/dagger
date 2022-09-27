---
slug: /hbf3z/extensions
displayed_sidebar: '0.3'
---

# Dagger Extensions

Pipelines can be shared and reused using Dagger *extensions*.

An extension is a collection of pipelines which can be imported into any Dagger project, and used to compose larger pipelines in the usual manner.

Extensions may themselves import other extensions.

```mermaid
graph LR;

script["your script"] -. Dagger API ..-> engine["Dagger Engine"]

subgraph A["your build pipeline"]
  A1[" "] -.-> A2["build with NPM"] -.-> A3[" "]
end

  A2 -.-> B1
    B4 -.-> A2
subgraph p2["Extension: NPM"]
      B1[" "] -.-> B2[" "] -.-> B3[" "] -.-> B4[" "]
    end
engine -..-> A1
```

[Learn more about writing extensions](../guides/bnzm7-extensions.md)
