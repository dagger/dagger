import http.client


def main():
    host = "server:8081"
    conn = http.client.HTTPConnection(host)
    conn.request("GET", "/hey", headers={"Host": host}) # BUG
    response = conn.getresponse()
    print(get_response_msg(response.read().decode(), response.status))

def get_response_msg(response_body, response_status):
    if response_status != 200:
        raise RuntimeError(f"Failed to say hello to server: {response_body}")
    return f"Server says: {response_body}"
