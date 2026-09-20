package idtui

import (
	"context"
	"fmt"
	"strings"
)

const (
	maxPromptImages     = 8
	maxPromptImageBytes = 20 << 20
)

func (fe *frontendPretty) acceptsPromptImages() bool {
	if _, ok := fe.shell.(PromptInputHandler); !ok {
		return false
	}
	mode, ok := fe.shell.(interface{ PromptMode() bool })
	return ok && mode.PromptMode()
}

// pastePromptImage reads the local clipboard only on an explicit keypress. The
// sequence guard prevents a slow helper from attaching to a cleared draft or a
// different agent after focus changes.
func (fe *frontendPretty) pastePromptImage() {
	if fe.imagePasting || !fe.acceptsPromptImages() {
		return
	}
	if len(fe.promptImages) >= maxPromptImages {
		fe.setPromptError(fmt.Errorf("a prompt can contain at most %d images", maxPromptImages))
		return
	}
	ctx := fe.shellCtx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	fe.imagePasteSeq++
	seq := fe.imagePasteSeq
	fe.imagePasteCancel = cancel
	fe.imagePasting = true
	fe.clearPromptError()
	fe.syncPrompt()
	read := fe.clipboardImage
	if read == nil {
		read = readClipboardImage
	}
	go func() {
		defer cancel()
		image, err := read(ctx)
		fe.dispatch(func() {
			if seq != fe.imagePasteSeq || fe.textInput == nil {
				return
			}
			fe.imagePasting = false
			fe.imagePasteCancel = nil
			if err == nil {
				err = fe.appendPromptImage(image)
			}
			if err != nil {
				fe.setPromptError(err)
			} else {
				fe.clearPromptError()
			}
			fe.syncPrompt()
			fe.Update()
		})
	}()
}

func (fe *frontendPretty) appendPromptImage(image PromptImage) error {
	if len(fe.promptImages) >= maxPromptImages {
		return fmt.Errorf("a prompt can contain at most %d images", maxPromptImages)
	}
	if len(image.Data) == 0 || !strings.HasPrefix(image.MIMEType, "image/") {
		return fmt.Errorf("clipboard did not contain an image")
	}
	total := len(image.Data)
	for _, existing := range fe.promptImages {
		total += len(existing.Data)
	}
	if total > maxPromptImageBytes {
		return fmt.Errorf("prompt images exceed the 20 MiB limit")
	}
	fe.promptImages = append(fe.promptImages, image)
	return nil
}

func (fe *frontendPretty) cancelImagePaste() {
	fe.imagePasteSeq++
	if fe.imagePasteCancel != nil {
		fe.imagePasteCancel()
		fe.imagePasteCancel = nil
	}
	fe.imagePasting = false
}

// validatePromptInput runs before clearing the editor, so unsupported input
// leaves both text and images available for correction rather than dropping
// attachments or accidentally executing a shell command.
func (fe *frontendPretty) validatePromptInput() error {
	if fe.imagePasting {
		return fmt.Errorf("wait for the clipboard image to finish loading")
	}
	if len(fe.promptImages) == 0 {
		return nil
	}
	if !fe.acceptsPromptImages() {
		return fmt.Errorf("image attachments can only be sent in prompt mode")
	}
	text := strings.TrimSpace(fe.textInput.Value())
	if strings.HasPrefix(text, "/") || text == "exit" {
		return fmt.Errorf("remove image attachments before running a command")
	}
	return nil
}
