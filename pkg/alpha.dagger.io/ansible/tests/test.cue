package ansible

AnsibleConfig: #AnsibleConfig

Ansible: #Ansible & {
	setup: AnsibleConfig
}

Inventory: #Inventory

SSH: #SSHKeys

PlaybookConfig: #PlaybookConfig & {
	ansibleConfig: AnsibleConfig
	inventory:     Inventory
	playbookFile:  "project/main.yml"
	sshKeys:       SSH
}

Playbook: #Playbook & {
	config: PlaybookConfig
	extraVars: ["person2=Dagger"]
	outputPath: "/tmp/output.yml"
}
