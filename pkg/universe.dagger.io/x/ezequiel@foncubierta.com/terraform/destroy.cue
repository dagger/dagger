package terraform

// Run `terraform destroy`
#Destroy: #Run & {
  cmd: "destroy"
  withinCmdArgs: ["-auto-approve"]
}