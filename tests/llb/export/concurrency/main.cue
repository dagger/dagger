package testing

test1: {
	string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
					echo lol1 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		{
			do:     "export"
			source: "/tmp/out"
			format: "string"
		},
	]
}

test2: {
	string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
					echo lol2 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		{
			do:     "export"
			source: "/tmp/out"
			format: "string"
		},
	]
}

test3: {
	string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
					echo lol3 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		{
			do:     "export"
			source: "/tmp/out"
			format: "string"
		},
	]
}

test4: {
	string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
					echo lol4 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		{
			do:     "export"
			source: "/tmp/out"
			format: "string"
		},
	]
}

test5: {
	string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
					echo lol5 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		{
			do:     "export"
			source: "/tmp/out"
			format: "string"
		},
	]
}

test6: {
	string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
					echo lol6 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		{
			do:     "export"
			source: "/tmp/out"
			format: "string"
		},
	]
}

test7: {
	string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
					echo lol7 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		{
			do:     "export"
			source: "/tmp/out"
			format: "string"
		},
	]
}

test8: {
	string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
					echo lol8 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		{
			do:     "export"
			source: "/tmp/out"
			format: "string"
		},
	]
}

test9: {
	string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
					echo lol9 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		{
			do:     "export"
			source: "/tmp/out"
			format: "string"
		},
	]
}

test10: {
	string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
					echo lol10 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		{
			do:     "export"
			source: "/tmp/out"
			format: "string"
		},
	]
}
