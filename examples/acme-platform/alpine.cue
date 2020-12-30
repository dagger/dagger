package alpine


#Image: {

	version: string | *"latest"
	packages: [...string]
	
	#dag: {
		do: [
				{
					//
					//
					// fetch alpine
				},
				{
					for _, pkg in packages {
	
					}
				}
	
		]
	}
}
