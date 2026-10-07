package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/client"
)

// The poll interval. A turn is seconds to minutes; a page is one request.
var followEvery = 2 * time.Second

type detail struct {
	Session struct {
		Status string `json:"status"`
	} `json:"session"`
	Turns []struct {
		Events []struct {
			ID      string  `json:"id"`
			Kind    string  `json:"kind"`
			Content *string `json:"content"`
			Tool    *string `json:"tool"`
		} `json:"events"`
	} `json:"turns"`
}

// Follow prints a session's events as they land and returns when the turn
// ends. The channels are internal, so this polls the messages page.
func Follow(api func() (*client.Client, error)) *cobra.Command {
	return &cobra.Command{
		Use:          "follow <id>",
		Short:        "Print a session's events until its turn ends",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			cl, err := api()
			if err != nil {
				return err
			}
			return follow(cl, args[0], c.OutOrStdout())
		},
	}
}

func follow(api *client.Client, id string, out io.Writer) error {
	seen := map[string]bool{}
	running := false
	for {
		_, raw, err := api.JSON("GET", "/api/v1/sessions/"+id+"/messages", nil, nil)
		if err != nil {
			return err
		}
		var d detail
		if err := json.Unmarshal(raw, &d); err != nil {
			return fmt.Errorf("the answer is not a session")
		}
		for _, turn := range d.Turns {
			for _, e := range turn.Events {
				if seen[e.ID] {
					continue
				}
				seen[e.ID] = true
				switch {
				case e.Tool != nil && *e.Tool != "":
					fmt.Fprintf(out, "tool: %s\n", *e.Tool)
				case e.Content != nil && *e.Content != "":
					fmt.Fprintf(out, "%s: %s\n", e.Kind, *e.Content)
				}
			}
		}
		switch d.Session.Status {
		case "running":
			running = true
		case "waiting":
			fmt.Fprintln(out, "waiting on an approval")
			return nil
		case "idle":
			if running {
				return nil
			}
		}
		time.Sleep(followEvery)
	}
}
