package docker

import (
"alpha.dagger.io/dagger"
"alpha.dagger.io/dagger/op"
)

TestSourceBuild: dagger.#Artifact & dagger.#Input

TestBuild: {
image: #Build & {
source: TestSourceBuild
}

verify: #up: [
op.#Load & {
from: image
},

op.#Exec & {
always: true
args: [
"sh", "-c", """
grep -q "test" /test.txt
""",
]
},
]
}

TestBuildWithArgs: {
image: #Build & {
dockerfile: """
FROM alpine
ARG TEST
ENV TEST=$TEST
RUN echo "$TEST" > /test.txt
"""
source: ""
args: TEST: "test"
}

verify: #up: [
op.#Load & {
from: image
},

op.#Exec & {
always: true
args: [
"sh", "-c", """
grep -q "test" /test.txt
""",
]
},
]
}

TestSourceImageFromDockerfile: dagger.#Artifact & dagger.#Input

TestImageFromDockerfile: {
image: #Build & {
dockerfile: """
FROM alpine
COPY test.txt /test.txt
"""
source: TestSourceImageFromDockerfile
}

verify: #up: [
op.#Load & {
from: image
},

op.#Exec & {
always: true
args: [
"sh", "-c", """
grep -q "test" /test.txt
""",
]
},
]
}
