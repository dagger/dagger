package daggercmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dagger/dagger/internal/hotline"
	"github.com/spf13/cobra"
)

func pageFlags(cmd *cobra.Command, o *hotline.PageOptions) {
	cmd.Flags().Int64Var(&o.After, "after", 0, "Continue from the next_cursor in the previous result")
	cmd.Flags().IntVar(&o.Limit, "limit", 20, "Maximum results (1–100)")
}

func (a *hotlineCLI) listCommand(name string) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: "List threads by latest message", Args: cobra.NoArgs}
	if name == "inbox" {
		cmd.Short = "List threads with unread messages"
	}
	if name == "queue" {
		cmd.Short = "List threads waiting for the Dagger team across all organizations"
		cmd.Long = "List threads whose last message came from a customer, across all organizations.\nReading leaves them in the queue. A team reply removes them.\nRequires support:admin permission. Results are ordered by latest message."
	}
	o := hotline.PageOptions{Unread: name == "inbox", Queue: name == "queue"}
	pageFlags(cmd, &o)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := o.Validate(); err != nil {
			return err
		}
		page, err := a.list(cmd, o)
		if err != nil {
			return err
		}
		if a.json {
			return json.NewEncoder(a.out).Encode(page)
		}
		var b bytes.Buffer
		if len(page.Threads) == 0 {
			if o.Unread {
				fmt.Fprintln(&b, "No unread messages.")
			} else if o.Queue {
				fmt.Fprintln(&b, "No threads waiting for a reply.")
			} else {
				fmt.Fprintln(&b, "No threads.")
			}
		} else {
			w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "THREAD\tORGANIZATION\tUNREAD\tLAST MESSAGE\tFROM\tPREVIEW")
			for _, t := range page.Threads {
				fmt.Fprintf(w, "%d\t%s\t%d\t%s\t%s\t%s\n", t.ID, clean(t.OrgName), t.UnreadCount, t.UpdatedAt.Format(time.RFC3339), sender(t.LastAuthorID, t.LastTeam), strings.Join(strings.Fields(clean(t.Preview)), " "))
			}
			w.Flush()
		}
		if page.NextCursor != 0 {
			fmt.Fprintf(&b, "\nMore threads: repeat with --after %d.\n", page.NextCursor)
		}
		_, err = io.Copy(a.out, &b)
		return err
	}
	return cmd
}

func (a *hotlineCLI) list(cmd *cobra.Command, o hotline.PageOptions) (hotline.ThreadPage, error) {
	var page hotline.ThreadPage
	path := "/v1/hotline/threads"
	if o.Queue {
		if a.org != "" {
			return page, fmt.Errorf("The team queue covers all organizations; omit --org.")
		}
		path = "/v1/hotline/queue"
	}
	path += fmt.Sprintf("?after=%d&limit=%d", o.After, o.Limit)
	if o.Unread {
		path += "&unread=true"
	}
	err := a.request(cmd, http.MethodGet, path, nil, "", &page)
	return page, err
}

func sender(author string, team bool) string {
	if team {
		return "Dagger team"
	}
	return strings.Join(strings.Fields(clean(author)), " ")
}

func (a *hotlineCLI) readCommand() *cobra.Command {
	var o hotline.PageOptions
	var peek bool
	cmd := &cobra.Command{Use: "read THREAD", Short: "Read messages and mark the returned messages as read", Args: cobra.ExactArgs(1)}
	pageFlags(cmd, &o)
	cmd.Flags().BoolVar(&o.Unread, "unread", false, "Show only unread messages from other accounts")
	cmd.Flags().BoolVar(&peek, "peek", false, "Leave messages unread")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}
		if err = o.Validate(); err != nil {
			return err
		}
		var c hotline.Conversation
		path := fmt.Sprintf("/v1/hotline/threads/%d", id)
		if err = a.request(cmd, http.MethodGet, fmt.Sprintf("%s?after=%d&limit=%d&unread=%t", path, o.After, o.Limit, o.Unread), nil, "", &c); err != nil {
			return err
		}
		var b bytes.Buffer
		if a.json {
			if err = json.NewEncoder(&b).Encode(c); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(&b, "Thread %d · %s · %s\n", id, clean(c.Thread.OrgName), clean(c.Thread.Workspace))
			if len(c.Messages) == 0 {
				if o.Unread {
					fmt.Fprintln(&b, "No unread messages.")
				} else {
					fmt.Fprintln(&b, "No messages.")
				}
			}
			for _, m := range c.Messages {
				fmt.Fprintf(&b, "\n[%d] %s · %s\n%s\n", m.ID, sender(m.AuthorID, m.Team), m.CreatedAt.Format(time.RFC3339), clean(m.Body))
				for _, f := range m.Attachments {
					fmt.Fprintf(&b, "  Attached: %s (%d bytes)\n", clean(f.Path), f.Size)
				}
				if len(m.Attachments) > 0 {
					fmt.Fprintf(&b, "  Use the attachments command to download message %d.\n", m.ID)
				}
			}
			if c.NextCursor != 0 {
				fmt.Fprintf(&b, "\nMore messages: repeat with --after %d.\n", c.NextCursor)
			}
		}
		if _, err = io.Copy(a.out, &b); err != nil {
			return err
		}
		if !peek && len(c.Messages) > 0 {
			ack := hotline.ReadRange{After: c.After, Through: c.Messages[len(c.Messages)-1].ID}
			var result struct{}
			return a.request(cmd, http.MethodPost, path+"/read", ack, "", &result)
		}
		return nil
	}
	return cmd
}

func (a *hotlineCLI) watchCommand() *cobra.Command {
	var queue bool
	cmd := &cobra.Command{Use: "watch", Short: "Report unread threads, then wait for new messages", Args: cobra.NoArgs,
		Long: "Report unread threads, then check every 5 seconds. Read positions do not change.\nWith --queue, watch threads waiting for a team reply, including already-read threads.\nUse --json for one thread per line. Stop with Ctrl-C."}
	cmd.Flags().BoolVar(&queue, "queue", false, "Watch the shared team queue across all organizations")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		seen := map[int64]int64{}
		delay := 5 * time.Second
		for {
			err := a.watchPoll(cmd, seen, queue)
			if cmd.Context().Err() != nil {
				return nil
			}
			if err != nil {
				var apiErr *hotline.Error
				if !errors.As(err, &apiErr) || (apiErr.Status < 500 && apiErr.Status != http.StatusTooManyRequests) {
					return err
				}
				if a.json {
					json.NewEncoder(a.errOut).Encode(map[string]any{"error": apiErr, "retry_seconds": delay.Seconds()})
				} else {
					fmt.Fprintf(a.errOut, "%s Retrying in %s.\n", clean(err.Error()), delay)
				}
			} else {
				delay = 5 * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-cmd.Context().Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
			if err != nil {
				delay = min(delay*2, time.Minute)
			}
		}
	}
	return cmd
}

func (a *hotlineCLI) watchPoll(cmd *cobra.Command, seen map[int64]int64, queue bool) error {
	o := hotline.PageOptions{Limit: 100, Unread: !queue, Queue: queue}
	for {
		page, err := a.list(cmd, o)
		if err != nil {
			return err
		}
		for _, t := range page.Threads {
			messageID := t.LastUnreadID
			if queue {
				messageID = t.LastMessageID
			}
			if messageID <= seen[t.ID] {
				continue
			}
			if a.json {
				err = json.NewEncoder(a.out).Encode(t)
			} else if queue {
				_, err = fmt.Fprintf(a.out, "Thread %d (%s): waiting for a team reply.\n", t.ID, clean(t.OrgName))
			} else {
				_, err = fmt.Fprintf(a.out, "Thread %d: %d unread.\n", t.ID, t.UnreadCount)
			}
			if err != nil {
				return err
			}
			seen[t.ID] = messageID
		}
		if page.NextCursor == 0 {
			return nil
		}
		o.After = page.NextCursor
	}
}
