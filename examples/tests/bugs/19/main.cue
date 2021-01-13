package test

#dagger: compute: [
    {
        do: "fetch-container"
        ref: "busybox"
    },
    {
        do: "exec"
        args: ["sh", "-c", """
            echo lol > /tmp/out
        """]
//				dir: "/"
    },
]
