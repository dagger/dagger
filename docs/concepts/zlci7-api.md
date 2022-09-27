---
slug: /zlci7/api
displayed_sidebar: '0.3'
---

# Dagger API

The Dagger API is a GraphQL API for composing and running powerful pipelines with minimal effort. By relying on the Dagger API to do the heavy lifting, you can write a small script that orchestrates a complex workflow, knowing that it will run in a secure and scalable way out of the box, and can easily be changed later as needed.

```mermaid
graph LR;

subgraph script["Your script"]

  code["your code"] -..-> client["client"] -..-> api
  subgraph engine["Dagger Engine"]
    api((Dagger API))
    subgraph A["build"]
      A1["step"] -.-> A2["step"] -.-> A3["step"]
    end
    subgraph B["deploy"]
      B1["step"] -.-> B2["step"] -.-> B3["step"] -.-> B4["step"]
    end
    subgraph C["test"]
      C1["step"] -.-> C2["step"] -.-> C3["step"] -.-> C4["step"]
    end
    api -..-> A1 & B1 & C1

  end
end
```