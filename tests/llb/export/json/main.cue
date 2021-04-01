package testing

testScalar: {
	bool

	#compute: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
				echo true > /tmp/out
				""",
			]
			dir: "/"
		},
		{
			do: "export"
			// Source path in the container
			source: "/tmp/out"
			format: "json"
		},
	]
}

testMap: #compute: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["sh", "-c", """
			echo '{"something": "something"}' > /tmp/out
			""",
		]
		dir: "/"
	},
	{
		do: "export"
		// Source path in the container
		source: "/tmp/out"
		format: "json"
	},
]

// FIXME: lists are currently broken
// testList: {
//  [...string]

//  #compute: [
//   {
//    do:  "fetch-container"
//    ref: "alpine"
//   },
//   {
//    do: "exec"
//    args: ["sh", "-c", """
//     echo '["milk", "pumpkin pie", "eggs", "juice"]' > /tmp/out
//     """,
//    ]
//    dir: "/"
//   },
//   {
//    do: "export"
//    // Source path in the container
//    source: "/tmp/out"
//    format: "json"
//   },
//  ]
// }
