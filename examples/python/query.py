from dagger import Engine

if __name__ == "__main__":
    query = """
    {
        core {
            image(ref: "alpine") {
            exec(input: { args: ["apk", "add", "curl"] }) {
                fs {
                exec(input: { args: ["curl", "https://dagger.io/"] }) {
                    stdout(lines: 1)
                }
                }
            }
            }
        }
    }
    """

    with Engine(workdir='../..') as client:
        client.do(query)
