package terraform

// Run `terraform fmt`
#Fmt: {

	// The Version of Terraform cli to use
	version?: string

	#Run & {
		// Terraform `fmt` command
		cmd: "fmt"

		"version": version
	}
}
