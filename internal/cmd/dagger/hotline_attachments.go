package daggercmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/internal/hotline"
	"github.com/spf13/cobra"
)

func collectFiles(paths []string) ([]hotline.File, error) {
	files := []hotline.File{}
	if len(paths) == 0 {
		return files, nil
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	total := 0
	for _, name := range paths {
		name = filepath.ToSlash(filepath.Clean(name))
		if err := hotline.ValidateFiles([]hotline.File{{Path: name}}); err != nil {
			return nil, err
		}
		info, err := root.Stat(name)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("Attachment %q must be a regular file inside this directory.", name)
		}
		f, err := root.Open(name)
		if err != nil {
			return nil, fmt.Errorf("Cannot open attachment %q.", name)
		}
		info, err = f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			return nil, fmt.Errorf("Attachment %q is not a regular file.", name)
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(hotline.MaxAttachmentBytes-total+1)))
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("Cannot read attachment %q.", name)
		}
		total += len(data)
		files = append(files, hotline.File{Path: name, Data: data})
		if err = hotline.ValidateFiles(files); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func (a *hotlineCLI) attachmentsCommand() *cobra.Command {
	var output string
	cmd := &cobra.Command{Use: "attachments MESSAGE --output FILE.zip", Short: "Download the selected workspace files for a message", Args: cobra.ExactArgs(1)}
	cmd.Flags().StringVar(&output, "output", "", "Required: new ZIP file to write")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}
		if output == "" {
			return fmt.Errorf("Supply --output FILE.zip.")
		}
		var bundle bytes.Buffer
		if err = a.request(cmd, http.MethodGet, fmt.Sprintf("/v1/hotline/messages/%d/attachments", id), nil, "", &bundle); err != nil {
			return err
		}
		f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("Cannot create output file. Use a new file path.")
		}
		_, err = f.Write(bundle.Bytes())
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			os.Remove(output)
			return fmt.Errorf("Cannot write the attachment bundle.")
		}
		if a.json {
			return json.NewEncoder(a.out).Encode(map[string]string{"output": output})
		}
		fmt.Fprintln(a.out, "Saved", clean(output))
		return nil
	}
	return cmd
}
