package tui

import (
	"errors"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/shlex"
)

// I'm sure these 10 lines are controversial to some. If you find yourself
// caring about this, just set $EDITOR!
var editors = []string{
	// graphical editors
	"code",
	"subl",
	"gedit",
	"nodepad++",

	// editors that mere mortals might not remember how to exit
	//
	// also, editors that might not take kindly to being run from within a
	// terminal in *another* editor
	"vim",
	"vi",
	"emacs",
	"helix",

	// everyone has these, right?
	"nano",
	"pico",
}

func openEditor(filePath string) tea.Cmd {
	editorCmd := os.Getenv("EDITOR")
	if editorCmd == "" {
		for _, editor := range editors {
			if _, err := exec.LookPath(editor); err == nil {
				editorCmd = editor
				break
			}
		}
	}

	if editorCmd == "" {
		return func() tea.Msg {
			return EditorExitMsg{errors.New("no $EDITOR available")}
		}
	}

	editorArgs, err := shlex.Split(editorCmd)
	if err != nil {
		return func() tea.Msg {
			return EditorExitMsg{err}
		}
	}
	editorArgs = append(editorArgs, filePath)

	cmd := exec.Command(editorArgs[0], editorArgs[1:]...) //nolint:gosec
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return EditorExitMsg{err}
	})
}
