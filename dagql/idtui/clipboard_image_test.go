package idtui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func clipboardTestPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func clipboardTestReader(goos string, env map[string]string, run func(context.Context, string, []string, int) ([]byte, error)) clipboardImageReader {
	return clipboardImageReader{
		goos:     goos,
		getenv:   func(key string) string { return env[key] },
		lookPath: func(name string) (string, error) { return name, nil },
		run:      run,
	}
}

func TestClipboardImageLinux(t *testing.T) {
	data := clipboardTestPNG(t)
	for _, tt := range []struct {
		name, helper        string
		env                 map[string]string
		listArgs, imageArgs []string
	}{
		{"wayland", "wl-paste", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, []string{"--list-types"}, []string{"--no-newline", "--type", "image/png"}},
		{"x11", "xclip", map[string]string{"DISPLAY": ":0"}, []string{"-selection", "clipboard", "-out", "-target", "TARGETS"}, []string{"-selection", "clipboard", "-out", "-target", "image/png"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			r := clipboardTestReader("linux", tt.env, func(ctx context.Context, helper string, args []string, limit int) ([]byte, error) {
				calls++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 3*time.Second {
					t.Fatal("clipboard command lacks bounded deadline")
				}
				if helper != tt.helper {
					t.Fatalf("helper = %q", helper)
				}
				if calls == 1 {
					if !reflect.DeepEqual(args, tt.listArgs) || limit != clipboardTypesMaxBytes {
						t.Fatalf("list command = %v, limit %d", args, limit)
					}
					return []byte("text/plain\nimage/jpeg\nimage/png\n"), nil
				}
				if !reflect.DeepEqual(args, tt.imageArgs) || limit != clipboardImageMaxBytes {
					t.Fatalf("image command = %v, limit %d", args, limit)
				}
				return data, nil
			})
			got, err := r.read(context.Background())
			if err != nil || got.MIMEType != "image/png" || !bytes.Equal(got.Data, data) || calls != 2 {
				t.Fatalf("image read: mime %q, calls %d, error %v", got.MIMEType, calls, err)
			}
		})
	}
}

func TestClipboardImageLinuxNeverRequestsText(t *testing.T) {
	for _, targets := range []string{"", "text/plain\nUTF8_STRING\n", "image/svg+xml\n", "image/png-malicious\n"} {
		t.Run(targets, func(t *testing.T) {
			calls := 0
			r := clipboardTestReader("linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, func(_ context.Context, _ string, args []string, _ int) ([]byte, error) {
				calls++
				if !reflect.DeepEqual(args, []string{"--list-types"}) {
					t.Fatal("requested clipboard contents without advertised image")
				}
				return []byte(targets), nil
			})
			_, err := r.read(context.Background())
			if !errors.Is(err, ErrClipboardNoImage) || calls != 1 {
				t.Fatalf("calls %d, error %v", calls, err)
			}
		})
	}
}

func TestClipboardImageLinuxFallback(t *testing.T) {
	r := clipboardTestReader("linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, func(_ context.Context, helper string, _ []string, _ int) ([]byte, error) {
		if helper != "xclip" {
			t.Fatal("did not fall back to available X11 helper")
		}
		return nil, nil
	})
	r.lookPath = func(name string) (string, error) {
		if name == "wl-paste" {
			return "", exec.ErrNotFound
		}
		return name, nil
	}
	if _, err := r.read(context.Background()); !errors.Is(err, ErrClipboardNoImage) {
		t.Fatal(err)
	}
}

func TestClipboardImageUnavailable(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows", "freebsd"} {
		t.Run(goos, func(t *testing.T) {
			r := clipboardTestReader(goos, map[string]string{"DISPLAY": ":0"}, func(context.Context, string, []string, int) ([]byte, error) {
				t.Fatal("ran missing helper")
				return nil, nil
			})
			r.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
			if _, err := r.read(context.Background()); err == nil || errors.Is(err, ErrClipboardNoImage) {
				t.Fatalf("unavailable clipboard must differ from no image: %v", err)
			}
		})
	}
}

func TestClipboardImageEncodedPlatforms(t *testing.T) {
	data := clipboardTestPNG(t)
	for _, tt := range []struct {
		goos, helper, encoded string
		limit                 int
	}{
		{"darwin", "osascript", "«data PNGf" + strings.ToUpper(hex.EncodeToString(data)) + "»\n", 2*clipboardImageMaxBytes + 64},
		{"windows", "powershell.exe", base64.StdEncoding.EncodeToString(data) + "\r\n", base64.StdEncoding.EncodedLen(clipboardImageMaxBytes) + 64},
	} {
		t.Run(tt.goos, func(t *testing.T) {
			r := clipboardTestReader(tt.goos, nil, func(_ context.Context, helper string, args []string, limit int) ([]byte, error) {
				if helper != tt.helper || limit != tt.limit {
					t.Fatalf("helper %q, limit %d", helper, limit)
				}
				if tt.goos == "darwin" && !reflect.DeepEqual(args, []string{"-e", clipboardPNGAppleScript}) {
					t.Fatalf("unexpected osascript command %v", args)
				}
				if tt.goos == "windows" && !reflect.DeepEqual(args, []string{"-NoProfile", "-NonInteractive", "-STA", "-Command", clipboardPNGPowerShell}) {
					t.Fatalf("unexpected PowerShell command %v", args)
				}
				return []byte(tt.encoded), nil
			})
			got, err := r.read(context.Background())
			if err != nil || got.MIMEType != "image/png" || !bytes.Equal(data, got.Data) {
				t.Fatalf("mime %q, error %v", got.MIMEType, err)
			}
		})
	}
}

func TestClipboardImageRejectInvalidAndOversized(t *testing.T) {
	for _, tt := range []struct {
		name, goos string
		data       []byte
		noImage    bool
	}{
		{"mac empty", "darwin", nil, true},
		{"windows empty", "windows", []byte("\r\n"), true},
		{"mac malformed", "darwin", []byte("sensitive clipboard text"), false},
		{"mac malformed hex", "darwin", []byte("«data PNGfxyz»"), false},
		{"mac odd hex", "darwin", []byte("«data PNGfA»"), false},
		{"windows malformed", "windows", []byte("sensitive clipboard text"), false},
		{"windows not image", "windows", []byte(base64.StdEncoding.EncodeToString([]byte("sensitive clipboard text"))), false},
		{"mac oversized", "darwin", bytes.Repeat([]byte("a"), 2*clipboardImageMaxBytes+65), false},
		{"windows oversized", "windows", bytes.Repeat([]byte("a"), base64.StdEncoding.EncodedLen(clipboardImageMaxBytes)+65), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := clipboardTestReader(tt.goos, nil, func(context.Context, string, []string, int) ([]byte, error) { return tt.data, nil })
			_, err := r.read(context.Background())
			if err == nil || errors.Is(err, ErrClipboardNoImage) != tt.noImage {
				t.Fatalf("unexpected error %v", err)
			}
			if strings.Contains(err.Error(), "sensitive") {
				t.Fatal("error leaked clipboard data")
			}
		})
	}
}

func TestClipboardImageValidation(t *testing.T) {
	for _, tt := range []struct {
		mimeType string
		encode   func(io.Writer, image.Image) error
	}{
		{"image/png", png.Encode},
		{"image/jpeg", func(w io.Writer, img image.Image) error { return jpeg.Encode(w, img, nil) }},
		{"image/gif", func(w io.Writer, img image.Image) error { return gif.Encode(w, img, nil) }},
	} {
		t.Run(tt.mimeType, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tt.encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
				t.Fatal(err)
			}
			got, err := validateClipboardImage(buf.Bytes(), tt.mimeType)
			if err != nil || got.MIMEType != tt.mimeType {
				t.Fatalf("mime %q, error %v", got.MIMEType, err)
			}
			if _, err := validateClipboardImage(buf.Bytes(), "image/wrong"); err == nil {
				t.Fatal("accepted MIME mismatch")
			}
			if _, err := validateClipboardImage(buf.Bytes()[:6], tt.mimeType); err == nil {
				t.Fatal("accepted truncated header")
			}
		})
	}
	// A one-pixel WebP fixture exercises the separately registered decoder.
	webp, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateClipboardImage(webp, "image/webp"); err != nil {
		t.Fatalf("WebP: %v", err)
	}
	if _, err := validateClipboardImage(make([]byte, clipboardImageMaxBytes+1), "image/png"); err == nil || !strings.Contains(err.Error(), "20 MiB") {
		t.Fatalf("oversized image: %v", err)
	}
	if _, err := validateClipboardImage([]byte("sensitive clipboard text"), "image/png"); err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("invalid image: %v", err)
	}
}

func TestClipboardImageCommandErrorsAndCancellation(t *testing.T) {
	r := clipboardTestReader("darwin", nil, func(context.Context, string, []string, int) ([]byte, error) {
		return nil, errors.New("sensitive clipboard content from stderr")
	})
	if _, err := r.read(context.Background()); err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("unsafe error: %v", err)
	}
	r.run = func(context.Context, string, []string, int) ([]byte, error) { return nil, errClipboardOutputTooLarge }
	if _, err := r.read(context.Background()); err == nil || !strings.Contains(err.Error(), "20 MiB") {
		t.Fatalf("size error: %v", err)
	}
	r.run = func(ctx context.Context, _ string, _ []string, _ int) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.read(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestClipboardOutputBound(t *testing.T) {
	out := &clipboardOutput{limit: 10}
	if _, ok := any(out).(io.ReaderFrom); ok {
		t.Fatal("ReaderFrom bypasses bounded Write")
	}
	if n, err := out.Write([]byte("12345678")); n != 8 || err != nil {
		t.Fatalf("write: %d, %v", n, err)
	}
	if n, err := out.Write([]byte("901")); n != 0 || !errors.Is(err, errClipboardOutputTooLarge) {
		t.Fatalf("overflow: %d, %v", n, err)
	}
	if out.buf.Len() != 8 {
		t.Fatal("retained oversized output")
	}
}

func TestClipboardEmptySelection(t *testing.T) {
	for _, tt := range []struct {
		helper, stderr string
		empty          bool
	}{
		{"wl-paste", "Nothing is copied\n", true},
		{"xclip", "Error: target TARGETS not available\n", true},
		{"wl-paste", "Failed to connect to a Wayland server", false},
		{"xclip", "Error: Can't open display: :0", false},
		{"wl-paste", "sensitive data: Nothing is copied", false},
		{"osascript", "Nothing is copied", false},
	} {
		if got := clipboardEmptySelection(tt.helper, tt.stderr); got != tt.empty {
			t.Errorf("helper %s: empty = %v, want %v", tt.helper, got, tt.empty)
		}
	}
}

func TestClipboardCommandBoundedProcess(t *testing.T) {
	// Spawn only this test executable, never a real clipboard helper.
	for _, mode := range []string{"ok", "overflow", "stderr", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("DAGGER_TEST_CLIPBOARD_PROCESS", mode)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			out, err := runClipboardCommand(ctx, os.Args[0], []string{"-test.run=^TestClipboardCommandProcess$"}, 10)
			switch mode {
			case "ok":
				if err != nil || string(out) != "12345" {
					t.Fatalf("command failed: %v", err)
				}
			case "overflow":
				if !errors.Is(err, errClipboardOutputTooLarge) {
					t.Fatalf("expected output bound: %v", err)
				}
			case "stderr":
				if err == nil || strings.Contains(err.Error(), "sensitive") || len(out) != 0 {
					t.Fatal("leaked command error")
				}
			case "cancel":
				if err == nil {
					t.Fatal("ignored cancellation")
				}
			}
		})
	}
}

func TestClipboardCommandProcess(t *testing.T) {
	switch os.Getenv("DAGGER_TEST_CLIPBOARD_PROCESS") {
	case "ok":
		_, _ = os.Stdout.WriteString("12345")
		os.Exit(0)
	case "overflow":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 4096))
		os.Exit(0)
	case "stderr":
		_, _ = os.Stderr.WriteString("sensitive clipboard data")
		os.Exit(1)
	}
}
