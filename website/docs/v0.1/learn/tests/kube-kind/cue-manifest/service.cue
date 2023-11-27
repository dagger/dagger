package main

// Service template containing all the common boilerplate shared by
// services of this application.
#Service: {
	// Name of the service. This will be used to label resources automatically
	// and generate selector.
	name: string

	// NodePort is the default service type.
	type: *"NodePort" | "LoadBalancer" | "ClusterIP" | "ExternalName"

	// Ports where the service should listen
	ports: [string]: number

	// Service manifest. Uses the name, type and ports above to
	// generate the resource manifest.
	manifest: {
		apiVersion: "v1"
		kind:       "Service"
		metadata: {
			"name": "\(name)-service"
			labels: app: name
		}
		spec: {
			"type": type
			"ports": [
				for k, v in ports {
					name: k
					port: v
				},
			]
			selector: app: name
		}
	}
}
