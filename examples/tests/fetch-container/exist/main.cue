package testing

// XXX https://github.com/blocklayerhq/dagger/issues/10 requires that #dagger are nested under https://github.com/blocklayerhq/dagger/issues/21 makes this very hard to verify

busybox1: {
	#dagger: compute: [
		{
			do: "fetch-container"
			ref: "busybox"
		},
	]
}

busybox2: {
	#dagger: compute: [
		{
			do: "fetch-container"
			ref: "busybox:latest"
		},
	]
}

busybox3: {
	#dagger: compute: [
		{
			do: "fetch-container"
			ref: "busybox:1.33-musl"
		},
	]
}

busybox4: {
	#dagger: compute: [
		{
			do: "fetch-container"
			ref: "busyboxa@sha256:e2af53705b841ace3ab3a44998663d4251d33ee8a9acaf71b66df4ae01c3bbe7"
		},
	]
}

busybox5: {
	#dagger: compute: [
		{
			do: "fetch-container"
			ref: "busybox:1.33-musl@sha256:e2af53705b841ace3ab3a44998663d4251d33ee8a9acaf71b66df4ae01c3bbe7"
		},
	]
}
