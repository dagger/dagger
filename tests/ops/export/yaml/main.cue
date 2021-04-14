package testing

import "dagger.io/dagger/op"

testScalar: {
	bool

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
				echo true > /tmp/out
				""",
			]
		},
		op.#Export & {
			// Source path in the container
			source: "/tmp/out"
			format: "yaml"
		},
	]
}

// FIXME: lists are currently broken
// testList: {
//  [...string]

//  #up: [
//   {
//    do:  "fetch-container"
//    ref: "alpine"
//   },
//   {
//    do: "exec"
//    args: ["sh", "-c", """
//     echo "--- # Shopping list
//     [milk, pumpkin pie, eggs, juice]" > /tmp/out
//     """,
//    ]
//    // XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
//    dir: "/"
//   },
//   {
//    do: "export"
//    // Source path in the container
//    source: "/tmp/out"
//    format: "yaml"
//   },
//  ]
// }

testMap: #up: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["sh", "-c", """
			echo something: something > /tmp/out
			""",
		]
	},
	{
		do: "export"
		// Source path in the container
		source: "/tmp/out"
		format: "yaml"
	},
]
