package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/client"
)

// The poll interval of a log followed: a build prints many lines a second, a rollout a line every few.
var linesEvery = 2 * time.Second

// A log that is followed: where its lines are read, the record its answer names, and what its statuses mean.
type followed struct {
	noun    string            // deployment, image, database
	path    string            // /api/v1/deployments/%s/logs
	ended   map[string]bool   // statuses that end it well
	waiting map[string]string // statuses that wait on someone, and what to tell
}

var deploymentLog = followed{
	noun:  "deployment",
	path:  "/api/v1/deployments/%s/logs",
	ended: map[string]bool{"live": true, "rolled_back": true, "denied": true},
	waiting: map[string]string{
		"waiting": "waiting for a person to allow it: nvoi deployment allow %s",
	},
}

var imageLog = followed{
	noun:    "image",
	path:    "/api/v1/images/%s/logs",
	ended:   map[string]bool{"ready": true, "pruned": true},
	waiting: map[string]string{},
}

// A production environment's database, named by its environment: made, or found again, at its vendor.
var databaseLog = followed{
	noun:    "database",
	path:    "/api/v1/environments/%s/database/logs",
	ended:   map[string]bool{"ready": true, "removed": true},
	waiting: map[string]string{},
}

// FollowLines prints a deployment's, an image's or a database's log lines as they are written and returns when it ends well, or
// an error when it failed.
func FollowLines(api func() (*client.Client, error), f followed) *cobra.Command {
	return &cobra.Command{
		Use:          "follow <id>",
		Short:        fmt.Sprintf("Print a %s's log lines until it ends", f.noun),
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			cl, err := api()
			if err != nil {
				return err
			}
			return followLines(cl, f, args[0], c.OutOrStdout())
		},
	}
}

func followLines(api *client.Client, f followed, id string, out io.Writer) error {
	since := ""
	told := false
	for {
		query := url.Values{}
		if since != "" {
			query.Set("since", since)
		}
		_, raw, err := api.JSON("GET", fmt.Sprintf(f.path, url.PathEscape(id)), query, nil)
		if err != nil {
			return err
		}
		var page map[string]json.RawMessage
		var record struct {
			Status string  `json:"status"`
			Error  *string `json:"error"`
		}
		var lines []struct {
			ID     string `json:"id"`
			Step   string `json:"step"`
			Output string `json:"output"`
		}
		if json.Unmarshal(raw, &page) != nil || json.Unmarshal(page[f.noun], &record) != nil ||
			json.Unmarshal(page["lines"], &lines) != nil || record.Status == "" {
			return fmt.Errorf("the answer is not a %s's log: %s", f.noun, string(raw))
		}
		for _, l := range lines {
			fmt.Fprintf(out, "%-8s %s\n", l.Step, l.Output)
			since = l.ID
		}
		switch {
		case f.ended[record.Status]:
			fmt.Fprintln(out, record.Status)
			return nil
		case record.Status == "failed":
			reason := ""
			if record.Error != nil {
				reason = ": " + *record.Error
			}
			return fmt.Errorf("the %s failed%s", f.noun, reason)
		case f.waiting[record.Status] != "" && !told:
			fmt.Fprintf(out, f.waiting[record.Status]+"\n", id)
			told = true
		}
		time.Sleep(linesEvery)
	}
}
