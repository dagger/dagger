package idtui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

// ErrClipboardNoImage means the clipboard is empty or has no supported image.
var ErrClipboardNoImage = errors.New("clipboard has no PNG, JPEG, WebP, or GIF image")

const (
	clipboardImageMaxBytes = 20 << 20
	clipboardImageTimeout  = 3 * time.Second
	clipboardTypesMaxBytes = 64 << 10
)

var errClipboardOutputTooLarge = errors.New("clipboard output exceeds limit")

type clipboardImageReader struct {
	goos     string
	getenv   func(string) string
	lookPath func(string) (string, error)
	run      func(context.Context, string, []string, int) ([]byte, error)
}

// readClipboardImage reads the local system clipboard. Call only in response to
// an explicit user image-paste action, never while polling or handling text paste.
func readClipboardImage(ctx context.Context) (PromptImage, error) {
	return (clipboardImageReader{
		goos: runtime.GOOS, getenv: os.Getenv, lookPath: exec.LookPath,
		run: runClipboardCommand,
	}).read(ctx)
}

func (r clipboardImageReader) read(ctx context.Context) (PromptImage, error) {
	ctx, cancel := context.WithTimeout(ctx, clipboardImageTimeout)
	defer cancel()

	var data []byte
	var mimeType string
	var err error
	switch r.goos {
	case "darwin":
		var out []byte
		out, err = r.command(ctx, "osascript", []string{"-e", clipboardPNGAppleScript}, 2*clipboardImageMaxBytes+64)
		if err == nil {
			data, err = decodeClipboardAppleScript(out)
		}
		mimeType = "image/png"
	case "linux":
		data, mimeType, err = r.readLinux(ctx)
	case "windows":
		var out []byte
		out, err = r.command(ctx, "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-STA", "-Command", clipboardPNGPowerShell}, base64.StdEncoding.EncodedLen(clipboardImageMaxBytes)+64)
		if err == nil {
			out = bytes.TrimSpace(out)
			if len(out) == 0 {
				err = ErrClipboardNoImage
			} else if len(out) > base64.StdEncoding.EncodedLen(clipboardImageMaxBytes) {
				err = clipboardImageTooLarge()
			} else {
				data = make([]byte, base64.StdEncoding.DecodedLen(len(out)))
				var n int
				n, err = base64.StdEncoding.Decode(data, out)
				data = data[:n]
				if err != nil {
					err = errors.New("clipboard helper returned invalid image data")
				}
			}
		}
		mimeType = "image/png"
	default:
		err = fmt.Errorf("clipboard image paste is not supported on %s", r.goos)
	}
	if err != nil {
		return PromptImage{}, err
	}
	return validateClipboardImage(data, mimeType)
}

func (r clipboardImageReader) readLinux(ctx context.Context) ([]byte, string, error) {
	var helper string
	var listArgs, imageArgs []string
	if r.getenv("WAYLAND_DISPLAY") != "" {
		if _, err := r.lookPath("wl-paste"); err == nil {
			helper = "wl-paste"
			listArgs = []string{"--list-types"}
			imageArgs = []string{"--no-newline", "--type"}
		}
	}
	if helper == "" && r.getenv("DISPLAY") != "" {
		if _, err := r.lookPath("xclip"); err == nil {
			helper = "xclip"
			listArgs = []string{"-selection", "clipboard", "-out", "-target", "TARGETS"}
			imageArgs = []string{"-selection", "clipboard", "-out", "-target"}
		}
	}
	if helper == "" {
		return nil, "", errors.New("clipboard image paste requires wl-paste (wl-clipboard) on Wayland or xclip on X11, and an active graphical session")
	}
	out, err := r.command(ctx, helper, listArgs, clipboardTypesMaxBytes)
	if err != nil {
		return nil, "", err
	}
	// Never request text or an unspecified target: helpers can otherwise choose
	// text when the clipboard does not contain an image.
	available := strings.Fields(string(out))
	for _, mimeType := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		for _, target := range available {
			if target == mimeType {
				data, err := r.command(ctx, helper, append(imageArgs, mimeType), clipboardImageMaxBytes)
				return data, mimeType, err
			}
		}
	}
	return nil, "", ErrClipboardNoImage
}

func (r clipboardImageReader) command(ctx context.Context, helper string, args []string, limit int) ([]byte, error) {
	path, err := r.lookPath(helper)
	if err != nil {
		return nil, fmt.Errorf("clipboard image paste requires %s; install or enable this helper", helper)
	}
	out, err := r.run(ctx, path, args, limit)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("clipboard image paste timed out or was canceled: %w", ctx.Err())
	}
	if errors.Is(err, errClipboardOutputTooLarge) {
		return nil, clipboardImageTooLarge()
	}
	if errors.Is(err, ErrClipboardNoImage) {
		return nil, ErrClipboardNoImage
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if helper == "powershell.exe" && exitErr.ExitCode() == 3 {
				return nil, clipboardImageTooLarge()
			}
		}
		// Command errors and stderr can contain clipboard contents. Do not
		// include either in the prompt's error message.
		return nil, fmt.Errorf("could not read clipboard image with %s; check access to the local graphical session", helper)
	}
	if len(out) > limit {
		return nil, clipboardImageTooLarge()
	}
	return out, nil
}

func clipboardImageTooLarge() error {
	return errors.New("clipboard image is too large (maximum 20 MiB)")
}

func validateClipboardImage(data []byte, expectedMIME string) (PromptImage, error) {
	if len(data) == 0 {
		return PromptImage{}, ErrClipboardNoImage
	}
	if len(data) > clipboardImageMaxBytes {
		return PromptImage{}, clipboardImageTooLarge()
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	mimeType := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "webp": "image/webp", "gif": "image/gif"}[format]
	if err != nil || config.Width <= 0 || config.Height <= 0 || mimeType == "" || mimeType != expectedMIME {
		return PromptImage{}, errors.New("clipboard did not contain a valid image of the advertised type")
	}
	return PromptImage{MIMEType: mimeType, Data: data}, nil
}

func decodeClipboardAppleScript(out []byte) ([]byte, error) {
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, ErrClipboardNoImage
	}
	const prefix = "«data PNGf"
	const suffix = "»"
	if !bytes.HasPrefix(out, []byte(prefix)) || !bytes.HasSuffix(out, []byte(suffix)) {
		return nil, errors.New("osascript returned invalid clipboard image data")
	}
	encoded := out[len(prefix) : len(out)-len(suffix)]
	if len(encoded) > 2*clipboardImageMaxBytes {
		return nil, clipboardImageTooLarge()
	}
	data := make([]byte, hex.DecodedLen(len(encoded)))
	n, err := hex.Decode(data, encoded)
	if err != nil {
		return nil, errors.New("osascript returned invalid clipboard image data")
	}
	return data[:n], nil
}

// osascript prints PNG clipboard data as «data PNGf<hex>». Coercion/empty
// clipboard errors mean no image, while other errors remain visible as failures.
const clipboardPNGAppleScript = `try
return the clipboard as «class PNGf»
on error number errNum
if errNum is -1700 or errNum is -1728 then
return ""
end if
error number errNum
end try`

const clipboardPNGPowerShell = `$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
if (-not [System.Windows.Forms.Clipboard]::ContainsImage()) { exit 0 }
$image = [System.Windows.Forms.Clipboard]::GetImage()
$stream = New-Object System.IO.MemoryStream
try {
  $image.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
  if ($stream.Length -gt 20971520) { exit 3 }
  [Console]::Out.Write([Convert]::ToBase64String($stream.ToArray()))
} finally {
  $stream.Dispose()
  $image.Dispose()
}`

// clipboardOutput bounds retained output, including encoded representations.
// Do not embed bytes.Buffer: its promoted ReadFrom would bypass bounded Write.
type clipboardOutput struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
}

func (b *clipboardOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buf.Len() {
		b.exceeded = true
		return 0, errClipboardOutputTooLarge
	}
	return b.buf.Write(p)
}

func runClipboardCommand(ctx context.Context, path string, args []string, limit int) ([]byte, error) {
	out := &clipboardOutput{limit: limit}
	stderr := &clipboardOutput{limit: 4096}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.WaitDelay = 100 * time.Millisecond
	err := cmd.Run()
	if out.exceeded {
		return nil, errClipboardOutputTooLarge
	}
	if err != nil {
		// Exit status 1 also means an inaccessible display. Recognize only
		// known empty-selection messages, never return stderr to the caller.
		if !stderr.exceeded && clipboardEmptySelection(filepath.Base(path), stderr.buf.String()) {
			return nil, ErrClipboardNoImage
		}
		return nil, err
	}
	return out.buf.Bytes(), nil
}

func clipboardEmptySelection(helper, stderr string) bool {
	switch helper {
	case "wl-paste":
		return strings.TrimSpace(stderr) == "Nothing is copied"
	case "xclip":
		return strings.TrimSpace(stderr) == "Error: target TARGETS not available"
	default:
		return false
	}
}
