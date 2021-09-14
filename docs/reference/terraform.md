---
sidebar_label: terraform
---

# alpha.dagger.io/terraform

Terraform operations

```cue
import "alpha.dagger.io/terraform"
```

## terraform.#Configuration

Terraform configuration

### terraform.#Configuration Inputs

| Name             | Type                  | Description            |
| -------------    |:-------------:        |:-------------:         |
|*version*         | `latest`              |Terraform version       |
|*source*          | `dagger.#Artifact`    |Source configuration    |

### terraform.#Configuration Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*output*          | `struct`          |-                   |
