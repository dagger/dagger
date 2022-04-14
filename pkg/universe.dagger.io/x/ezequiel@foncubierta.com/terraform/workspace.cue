package terraform

_#workspaceSubcmd: "new" | "select" | "delete"

// Run `terraform workspace`
#Workspace: #Run & {
  cmd: "workspace " + subCmd
  subCmd: _#workspaceSubcmd
}