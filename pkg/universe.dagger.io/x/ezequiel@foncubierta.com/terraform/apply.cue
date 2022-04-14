package terraform

// Run `terraform apply`
#Apply: #Run & {
  cmd: "apply"
  withinCmdArgs: ["-auto-approve", planFile]
  planFile: _#defaultPlanFile
}