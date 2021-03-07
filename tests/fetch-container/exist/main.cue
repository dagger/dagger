package testing

busybox1: #compute: [
	{
		do:  "fetch-container"
		ref: "busybox"
	},
]

busybox2: #compute: [
	{
		do:  "fetch-container"
		ref: "busybox:latest"
	},
]

busybox3: #compute: [
	{
		do:  "fetch-container"
		ref: "busybox:1.33-musl"
	},
]

busybox4: #compute: [
	{
		do:  "fetch-container"
		ref: "busybox@sha256:e2af53705b841ace3ab3a44998663d4251d33ee8a9acaf71b66df4ae01c3bbe7"
	},
]

busybox5: #compute: [
	{
		do:  "fetch-container"
		ref: "busybox:1.33-musl@sha256:e2af53705b841ace3ab3a44998663d4251d33ee8a9acaf71b66df4ae01c3bbe7"
	},
]
