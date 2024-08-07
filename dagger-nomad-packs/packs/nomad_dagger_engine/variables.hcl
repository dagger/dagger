variable "job_name" {
  # If "", the pack name will be used
  description = "The name to use as the job name which overrides using the pack name"
  type        = string
  default     = "dagger-engine"
}

variable "region" {
  description = "The region where jobs will be deployed"
  type        = string
  default     = ""
}

variable "datacenters" {
  description = "A list of datacenters in the region which are eligible for task placement"
  type        = list(string)
  default     = ["*"]
}

variable "node_pool" {
  description = "The pool to use for task placement"
  type        = string
  default     = ""
}

variable "dagger_image" {
  description = "The Dagger image to use for the job"
  type        = string
  default     = "registry.dagger.io/engine:v0.12.0"
}

variable "resources" {
  description = "The resource to assign to the dagger engine."
  type = object({
    cpu    = number
    memory = number
  })
  default = {
    cpu    = 500,
    memory = 4096
  }
}