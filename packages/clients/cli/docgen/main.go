// docgen writes the CLI's command pages into packages/core/site: a section per group,
// a heading per command with its usage line and flags. The overview beside it
// (reference/cli.mdx) is written by hand. The tree is the
// one the binary builds, so the page cannot name a command it does not have.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/cmd"
)

const preamble = `---
title: Commands
description: Every nvoi command, generated from the binary.
---

Install, contexts and how a command reads are in the [overview](/docs/reference/cli/).
`

func main() {
	out := flag.String("out", "../../core/site/src/content/docs/reference/cli/commands.mdx", "the page to write")
	flag.Parse()

	root, err := cmd.Root(&cmd.Resolver{All: true})
	if err != nil {
		log.Fatal(err)
	}

	var b strings.Builder
	b.WriteString(preamble)
	section(&b, "Authentication", leaves(root))
	for _, group := range groups(root) {
		section(&b, group.Name(), leaves(group))
	}
	if err := os.WriteFile(*out, []byte(b.String()), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println(*out)
}

func groups(root *cobra.Command) []*cobra.Command {
	var list []*cobra.Command
	for _, c := range root.Commands() {
		if c.HasSubCommands() {
			list = append(list, c)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name() < list[j].Name() })
	return list
}

// The commands under c that run something, in name order.
func leaves(c *cobra.Command) []*cobra.Command {
	var list []*cobra.Command
	for _, sub := range c.Commands() {
		if sub.HasSubCommands() || sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		list = append(list, sub)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name() < list[j].Name() })
	return list
}

func section(b *strings.Builder, title string, commands []*cobra.Command) {
	fmt.Fprintf(b, "\n## %s\n", title)
	for _, c := range commands {
		fmt.Fprintf(b, "\n### `%s`\n\n%s\n\n```bash\n%s\n```\n", c.CommandPath(), text(c.Short), usage(c))
		rows := flags(c)
		if len(rows) == 0 {
			continue
		}
		b.WriteString("\n| flag | | |\n| --- | --- | --- |\n")
		for _, r := range rows {
			fmt.Fprintf(b, "| `--%s` | %s | %s |\n", r.name, r.required, text(r.doc))
		}
	}
}

// MDX reads braces as expressions and < as a tag; a pipe would end the table cell.
var mdx = strings.NewReplacer("{", "\\{", "}", "\\}", "<", "&lt;", "|", "\\|")

func text(s string) string { return mdx.Replace(s) }

func usage(c *cobra.Command) string {
	line := c.CommandPath() + strings.TrimPrefix(c.Use, c.Name())
	if len(flags(c)) > 0 {
		line += " [flags]"
	}
	return line
}

type row struct{ name, required, doc string }

func flags(c *cobra.Command) []row {
	var rows []row
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" || f.Name == "table" {
			return
		}
		required := ""
		if f.Annotations[cobra.BashCompOneRequiredFlag] != nil {
			required = "required"
		}
		rows = append(rows, row{f.Name, required, f.Usage})
	})
	return rows
}
