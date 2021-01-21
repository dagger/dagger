package test

#dagger: compute: [
    {
        do: "fetch-container"
        ref: "busybox"
    },
    {
        do: "exec"
        args: ["sh", "-c", """
            echo hello > /tmp/out
        """]
//                dir: "/"
    },
]
