variable "SAN" {
  default = "127.0.0.1"
}

group "default" {
  targets = ["certs"]
}

target "certs" {
  args = {
    SAN = SAN
  }
  output = ["./.certs"]
}
