package protocol

import (
	"bytes"
	"encoding/json"
)

// ReadEnvelope reads a message as json.Unmarshal into an Envelope reads it,
// in fewer passes over its body. When the message's type comes before its
// body and the body is its last field, as NewEnvelope writes it, and bodyFor
// returns a value for the envelope read so far, the body is decoded straight
// from the message's bytes into that value, which ReadEnvelope returns, and
// the envelope's Body is nil. json.Unmarshal checks the whole message and
// copies the body, then Decode checks the body again before decoding it.
// Every other message, or one ReadEnvelope can't read so, is read by
// json.Unmarshal: its Body raw, the body nil, and Unmarshal's error.
func ReadEnvelope(data []byte, bodyFor func(Envelope) any) (Envelope, any, error) {
	if env, body, ok := readEnvelopeOnce(data, bodyFor); ok {
		return env, body, nil
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, nil, err
	}
	return env, nil, nil
}

// readEnvelopeOnce is ReadEnvelope's direct read. It reads the fields before
// the body with a decoder, and the body from the message's own bytes, between
// its key's colon and the message's closing brace, so the decoder's buffer
// never holds it. It reports false for any message it doesn't read exactly as
// json.Unmarshal would: a key other than the envelope's own, a repeated key,
// a body before the type, with no value to decode into or not last (more
// than one value before the closing brace), or anything malformed.
func readEnvelopeOnce(data []byte, bodyFor func(Envelope) any) (Envelope, any, bool) {
	var env Envelope
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return Envelope{}, nil, false
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		key, isKey := tok.(string)
		if err != nil || !isKey || seen[key] {
			return Envelope{}, nil, false
		}
		seen[key] = true
		switch key {
		case "id":
			err = dec.Decode(&env.ID)
		case "re":
			err = dec.Decode(&env.Re)
		case "type":
			err = dec.Decode(&env.Type)
		case "body":
			if !seen["type"] {
				return Envelope{}, nil, false
			}
			body := bodyFor(env)
			if body == nil {
				return Envelope{}, nil, false
			}
			start, end, ok := bodyBounds(data, dec.InputOffset())
			if !ok || json.Unmarshal(data[start:end], body) != nil {
				return Envelope{}, nil, false
			}
			return env, body, true
		default:
			return Envelope{}, nil, false
		}
		if err != nil {
			return Envelope{}, nil, false
		}
	}
	// A message with no body is json.Unmarshal's to read.
	return Envelope{}, nil, false
}

// bodyBounds returns where a body that is a message's last field lies: from
// after its key's colon, the key ending at offset, to the message's closing
// brace. Whether one value fills it is json.Unmarshal's to say.
func bodyBounds(data []byte, offset int64) (start, end int, ok bool) {
	start = int(offset)
	for start < len(data) && isSpace(data[start]) {
		start++
	}
	if start == len(data) || data[start] != ':' {
		return 0, 0, false
	}
	start++
	end = len(data)
	for end > start && isSpace(data[end-1]) {
		end--
	}
	if end == start || data[end-1] != '}' {
		return 0, 0, false
	}
	return start, end - 1, true
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
