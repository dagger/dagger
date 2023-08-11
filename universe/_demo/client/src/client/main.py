import http.client


def main():
    host = "server:8081"
    conn = http.client.HTTPConnection(host)
    conn.request("GET", "/hello", headers={"Host": host})
    response = conn.getresponse()
    print(response.read().decode())
