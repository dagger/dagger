// Command module-server is a module server that speaks the protocol of
// `python -m dagger.mod serve`, so the engine's tests need no Python SDK. Each
// result names the server instance and how many calls it has served.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// callFrame is decoded with the shapes the SDK's Module.dispatch reads: the
// receiver and the arguments are JSON text, so an object there fails the call.
type callFrame struct {
	Request struct {
		ReceiverType  string `json:"receiverType"`
		ReceiverValue string `json:"receiverValue"`
		FnName        string `json:"fnName"`
		FnArgs        string `json:"fnArgs"`
	} `json:"request"`
	ClientID string `json:"clientID"`
}

func main() {
	l, err := net.Listen("tcp", "127.0.0.1:8737")
	if err != nil {
		panic(err)
	}
	instance := rand.Text()
	for calls := 1; ; calls++ {
		conn, err := l.Accept()
		if err != nil {
			panic(err)
		}
		serve(conn, fmt.Sprintf("%s:%d", instance, calls))
		conn.Close()
	}
}

func serve(conn net.Conn, served string) {
	var header [5]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil || header[0] != 'C' {
		panic(fmt.Sprintf("read call frame: %v %q", err, header[0]))
	}
	payload := make([]byte, binary.BigEndian.Uint32(header[1:]))
	if _, err := io.ReadFull(conn, payload); err != nil {
		panic(err)
	}
	var call callFrame
	if err := json.Unmarshal(payload, &call); err != nil {
		panic(err)
	}
	switch call.Request.FnName {
	case "":
		write(conn, 'R', "{}")
	case "Served":
		write(conn, 'O', "served "+served+"\n")
		result(conn, served)
	case "Module":
		result(conn, query(call.ClientID, "{currentModule{name}}"))
	case "Fail":
		write(conn, 'E', "boom on stderr\n")
		write(conn, 'X', "boom")
	case "Crash":
		os.Exit(1)
	case "Slow":
		time.Sleep(5 * time.Second)
		result(conn, served)
	}
}

func query(clientID, q string) string {
	body, _ := json.Marshal(map[string]string{"query": q})
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+os.Getenv("DAGGER_SESSION_PORT")+"/query", strings.NewReader(string(body)))
	if err != nil {
		panic(err)
	}
	req.SetBasicAuth(os.Getenv("DAGGER_SESSION_TOKEN"), "")
	req.Header.Set("Content-Type", "application/json")
	metadata, _ := json.Marshal(map[string]string{"client_id": clientID})
	req.Header.Set("X-Dagger-Client-Metadata", base64.StdEncoding.EncodeToString(metadata))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	return string(out)
}

func result(conn net.Conn, value string) {
	out, _ := json.Marshal(value)
	write(conn, 'R', string(out))
}

func write(conn net.Conn, kind byte, payload string) {
	frame := make([]byte, 5, 5+len(payload))
	frame[0] = kind
	binary.BigEndian.PutUint32(frame[1:], uint32(len(payload)))
	if _, err := conn.Write(append(frame, payload...)); err != nil {
		panic(err)
	}
}
