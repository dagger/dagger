terraform {
  required_providers {
    random = {
      source = "hashicorp/random"
      version = "3.1.0"
    }
  }

  backend "s3" {
    bucket = "dagger-ci"
    key    = "terraform/tfstate"
    region = "us-east-2"
  }
}

provider "random" {
}

variable "input" {
  type = string
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
