// The dispatcher a Dang entrypoint execs for each call, as the Go SDK's
// generated dagger.dispatch.gen.go is (its engine-call mode, trimmed to the
// standard library): the call request comes on stdin, the result goes to the
// file named by the second argument.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// callRequest is the ModuleEntrypoint.call input. The entrypoint passes each
// JSON value as JSON text in a string; a JSON value is accepted too.
type callRequest struct {
	ReceiverType  string          `json:"receiverType"`
	ReceiverValue json.RawMessage `json:"receiverValue"`
	FnName        string          `json:"fnName"`
	FnArgs        json.RawMessage `json:"fnArgs"`
}

func main() {
	if len(os.Args) < 3 || os.Args[1] != "engine-call" {
		fatalf("usage: %s engine-call RESULT", os.Args[0])
	}
	if err := os.MkdirAll(filepath.Dir(os.Args[2]), 0o755); err != nil {
		fatalf("%v", err)
	}
	output, err := os.Create(os.Args[2])
	if err != nil {
		fatalf("%v", err)
	}
	if err := engineCall(os.Stdin, output); err != nil {
		fatalf("%v", err)
	}
	if err := output.Close(); err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	encoded, _ := json.Marshal(message)
	fmt.Fprintf(os.Stderr, "%s\n::dagger-function-error::%s\n", message, encoded)
	os.Exit(2)
}

func engineCall(input io.Reader, output io.Writer) error {
	var req callRequest
	if err := json.NewDecoder(input).Decode(&req); err != nil {
		return fmt.Errorf("decode call request: %w", err)
	}
	receiver, err := jsonValue("receiverValue", req.ReceiverValue)
	if err != nil {
		return err
	}
	fnArgs, err := jsonValue("fnArgs", req.FnArgs)
	if err != nil {
		return err
	}
	args := map[string]json.RawMessage{}
	if err := json.Unmarshal(fnArgs, &args); err != nil {
		return fmt.Errorf("fnArgs is not a JSON object: %w", err)
	}
	result, err := dispatch(receiver, req.ReceiverType, req.FnName, args)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func dispatch(receiver json.RawMessage, receiverType, fnName string, args map[string]json.RawMessage) (any, error) {
	if receiverType == "" && (fnName == "Grower" || fnName == "grower") {
		receiverType, fnName = "Grower", ""
	}
	if receiverType != "Grower" {
		return nil, fmt.Errorf("unknown object %s", receiverType)
	}
	var parent Grower
	if string(receiver) != "null" {
		if err := json.Unmarshal(receiver, &parent); err != nil {
			return nil, fmt.Errorf("unmarshal parent object: %w", err)
		}
	}
	switch fnName {
	case "", "New":
		return &Grower{}, nil
	case "Grow":
		var n int
		if err := json.Unmarshal(args["n"], &n); err != nil {
			return nil, fmt.Errorf("unmarshal input arg n: %w", err)
		}
		return parent.Grow(n), nil
	case "Size":
		return parent.Size(), nil
	default:
		return nil, fmt.Errorf("unknown function %s", fnName)
	}
}

// jsonValue returns the JSON value in a request field. A JSON string holds
// JSON text, so it is decoded once. A missing field reads as null.
func jsonValue(field string, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("null"), nil
	}
	if raw[0] != '"' {
		return raw, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, fmt.Errorf("decode %s: %w", field, err)
	}
	if !json.Valid([]byte(text)) {
		return nil, fmt.Errorf("%s is not valid JSON text", field)
	}
	return json.RawMessage(text), nil
}
