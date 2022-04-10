terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = "=3.1.2"
    }
  }
}

provider "random" {
}

variable "input" {
  type    = string
  default = "test-data"
}

resource "random_integer" "test" {
  min = 1
  max = 50
}

output "random" {
  value = random_integer.test.result
}

output "input" {
  value = var.input
}
