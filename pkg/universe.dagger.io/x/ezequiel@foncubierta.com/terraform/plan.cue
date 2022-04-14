package terraform

// File to write the output plan
_#defaultPlanFile: string | *"./out.tfplan"

// Run `terraform plan`
#Plan: #Run & {
  cmd: "plan"
  withinCmdArgs: ["-out=\(planFile)"]
  planFile: _#defaultPlanFile
}