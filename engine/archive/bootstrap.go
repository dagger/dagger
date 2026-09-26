package archive

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const BootstrapContentType = "application/vnd.dagger.telemetry.bootstrap"

// MaxBootstrapPayloadSize bounds the encoded payload of each bootstrap frame.
const MaxBootstrapPayloadSize = 64 << 20

var bootstrapMagic = [4]byte{'D', 'A', 'B', 1}

// ErrBootstrapIncomplete identifies a finite bootstrap response that ended
// before its terminal frame.
var ErrBootstrapIncomplete = errors.New("bootstrap stream ended before terminal frame")

type BootstrapFrameKind byte

const (
	BootstrapFrameHeader   BootstrapFrameKind = 1
	BootstrapFrameTraces   BootstrapFrameKind = 2
	BootstrapFrameLogs     BootstrapFrameKind = 3
	BootstrapFrameTerminal BootstrapFrameKind = 4
)

type BootstrapHeader struct {
	Version    int        `json:"version"`
	TraceID    string     `json:"traceID"`
	SealAt     string     `json:"sealAt"`
	HighWater  HighWater  `json:"highWater"`
	Completion Completion `json:"completion"`
}

type BootstrapSignal struct {
	Kind    BootstrapFrameKind
	Payload []byte
	Records int64
}

type BootstrapTerminal struct {
	TraceRecords int64  `json:"traceRecords"`
	LogRecords   int64  `json:"logRecords"`
	SHA256       string `json:"sha256"`
}

func BuildBootstrap(header BootstrapHeader, signals []BootstrapSignal) ([]byte, int64, error) {
	header.Version = ManifestVersion
	headerPayload, err := json.Marshal(header)
	if err != nil {
		return nil, 0, err
	}
	var output byteWriter
	hash := sha256.New()
	writeHashed := func(kind BootstrapFrameKind, payload []byte) error {
		if err := WriteBootstrapFrame(io.MultiWriter(&output, hash), kind, payload); err != nil {
			return err
		}
		return nil
	}
	if err := writeHashed(BootstrapFrameHeader, headerPayload); err != nil {
		return nil, 0, err
	}
	var terminal BootstrapTerminal
	for _, signal := range signals {
		if signal.Kind != BootstrapFrameTraces && signal.Kind != BootstrapFrameLogs {
			return nil, 0, fmt.Errorf("invalid bootstrap signal frame kind %d", signal.Kind)
		}
		if err := writeHashed(signal.Kind, signal.Payload); err != nil {
			return nil, 0, err
		}
		switch signal.Kind {
		case BootstrapFrameTraces:
			terminal.TraceRecords += signal.Records
		case BootstrapFrameLogs:
			terminal.LogRecords += signal.Records
		}
	}
	terminal.SHA256 = hex.EncodeToString(hash.Sum(nil))
	payload, err := json.Marshal(terminal)
	if err != nil {
		return nil, 0, err
	}
	if err := WriteBootstrapFrame(&output, BootstrapFrameTerminal, payload); err != nil {
		return nil, 0, err
	}
	return output, terminal.TraceRecords + terminal.LogRecords, nil
}

type byteWriter []byte

func (w *byteWriter) Write(p []byte) (int, error) { *w = append(*w, p...); return len(p), nil }

func WriteBootstrapFrame(w io.Writer, kind BootstrapFrameKind, payload []byte) error {
	if len(payload) > MaxBootstrapPayloadSize {
		return fmt.Errorf("bootstrap payload is %d bytes (maximum %d)", len(payload), MaxBootstrapPayloadSize)
	}
	var header [9]byte
	copy(header[:4], bootstrapMagic[:])
	header[4] = byte(kind)
	binary.BigEndian.PutUint32(header[5:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func ReadBootstrapFrame(r io.Reader) (BootstrapFrameKind, []byte, error) {
	var header [9]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	if [4]byte(header[:4]) != bootstrapMagic {
		return 0, nil, errors.New("invalid bootstrap frame magic")
	}
	kind := BootstrapFrameKind(header[4])
	if kind < BootstrapFrameHeader || kind > BootstrapFrameTerminal {
		return 0, nil, fmt.Errorf("invalid bootstrap frame kind %d", kind)
	}
	size := binary.BigEndian.Uint32(header[5:])
	if size > MaxBootstrapPayloadSize {
		return 0, nil, fmt.Errorf("bootstrap frame is %d bytes (maximum %d)", size, MaxBootstrapPayloadSize)
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return kind, payload, nil
}

// DecodeBootstrap requires a header first and a terminal last, rejects trailing
// bytes, verifies the terminal checksum, calls onHeader before reading any
// signal frame, and calls consume for each signal frame. EOF before the terminal
// is an interruption, never a successful finite response.
func DecodeBootstrap(r io.Reader, onHeader func(BootstrapHeader) error, consume func(BootstrapFrameKind, []byte) error) (BootstrapHeader, BootstrapTerminal, error) {
	var header BootstrapHeader
	var terminal BootstrapTerminal
	hash := sha256.New()
	seenHeader := false
	for {
		kind, payload, err := ReadBootstrapFrame(r)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return header, terminal, ErrBootstrapIncomplete
			}
			return header, terminal, err
		}
		if kind == BootstrapFrameTerminal {
			if !seenHeader {
				return header, terminal, errors.New("bootstrap terminal before header")
			}
			if err := json.Unmarshal(payload, &terminal); err != nil {
				return header, terminal, err
			}
			if terminal.SHA256 != hex.EncodeToString(hash.Sum(nil)) {
				return header, terminal, errors.New("bootstrap checksum mismatch")
			}
			var trailing [1]byte
			if n, err := r.Read(trailing[:]); n != 0 || (err != nil && !errors.Is(err, io.EOF)) {
				return header, terminal, errors.New("bootstrap has trailing data")
			}
			return header, terminal, nil
		}
		var frame byteWriter
		_ = WriteBootstrapFrame(&frame, kind, payload)
		_, _ = hash.Write(frame)
		if !seenHeader {
			if kind != BootstrapFrameHeader {
				return header, terminal, errors.New("bootstrap first frame is not header")
			}
			if err := json.Unmarshal(payload, &header); err != nil {
				return header, terminal, err
			}
			if header.Version != ManifestVersion {
				return header, terminal, fmt.Errorf("unsupported bootstrap version %d", header.Version)
			}
			seenHeader = true
			if onHeader != nil {
				if err := onHeader(header); err != nil {
					return header, terminal, err
				}
			}
			continue
		}
		if kind == BootstrapFrameHeader {
			return header, terminal, errors.New("duplicate bootstrap header")
		}
		if consume != nil {
			if err := consume(kind, payload); err != nil {
				return header, terminal, err
			}
		}
	}
}

// VerifyBootstrap validates a bootstrap stream without consuming its signal
// payloads.
func VerifyBootstrap(r io.Reader) (BootstrapHeader, BootstrapTerminal, error) {
	return DecodeBootstrap(r, nil, nil)
}
