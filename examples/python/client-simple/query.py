from gql import gql
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

    with Engine() as client:
        result = client.execute(gql(query))
        content = result['core']['image']['exec']['fs']['exec']['stdout']
        print(content)
