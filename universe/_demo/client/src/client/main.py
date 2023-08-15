import http.client


def main():
    host = "server:8081"
    conn = http.client.HTTPConnection(host)
    conn.request("GET", "/hey", headers={"Host": host})
    response = conn.getresponse()
    print(get_response_msg(response.read().decode(), response.status))

def get_response_msg(response_body, response_status):
    if response_status != 200:
        raise RuntimeError("Failed to say hello to server")
    return f"Server says: {response_body}"
