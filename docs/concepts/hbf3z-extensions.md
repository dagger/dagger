---
slug: /hbf3z/extensions
displayed_sidebar: '0.3'
---

# Dagger Extensions

Developers can extend the capabilities of Dagger with extensions.

Extensions can define custom steps, which developers can then incorporate into their pipelines. Of course, these custom steps may themselves be powered by Dagger pipelines, creating endless possibilities for component reuse and composition.

Extensions are fully sandboxed, so they can be safely shared and reused between projects.

```mermaid
graph LR;

subgraph script["Your script"]

  code["your code"] -..-> client["client"] -..-> api
  subgraph engine["Dagger Engine"]
    api((Dagger API))
    subgraph pipeline
      A1["step"] -.-> A2["build with npm"] -.-> A3["step"]
    end
    A2 -.-> B1
    B4 -.-> A2

    subgraph p2["NPM Extension"]
      B1["step"] -.-> B2["step"] -.-> B3["step"] -.-> B4["step"]
    end
  
    api <-..-> A1
  end
end
```

[Learn more about writing extensions](../guides/bnzm7-writing_extensions.md)