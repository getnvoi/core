package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/client"
)

// Tree adds one cobra command per operation under root, grouped by tag. The
// client is resolved per call, so a login is not asked for to print help. mode is
// the context's (empty when not known): an operation marked for another mode is
// hidden from help and completion, and so is a group left with none.
func Tree(root *cobra.Command, ops []Operation, api func() (*client.Client, error), mode string) {
	groups := map[string]*cobra.Command{}
	for _, op := range ops {
		group, ok := groups[op.Group]
		if !ok {
			group = &cobra.Command{Use: op.Group, Short: "The " + op.Group + " operations", Hidden: true}
			groups[op.Group] = group
			root.AddCommand(group)
		}
		c := command(op, api, mode)
		group.AddCommand(c)
		group.Hidden = group.Hidden && c.Hidden
	}
}

// elsewhere is whether the operation is served only by an installation of another mode than the context's.
func elsewhere(op Operation, mode string) bool {
	return op.Mode != "" && op.Mode != mode
}

func command(op Operation, api func() (*client.Client, error), mode string) *cobra.Command {
	use := op.Name
	for _, p := range op.Params {
		use += " <" + Named(op.Path, p) + ">"
	}
	c := &cobra.Command{
		Use:          use,
		Short:        op.Summary,
		Args:         cobra.ExactArgs(len(op.Params)),
		SilenceUsage: true,
		Hidden:       mode != "" && elsewhere(op, mode),
		RunE: func(c *cobra.Command, args []string) error {
			cl, err := api()
			if err != nil {
				return err
			}
			table, _ := c.Flags().GetBool("table")
			err = call(cl, op, args, c.Flags(), table, c.OutOrStdout())
			if elsewhere(op, mode) && unrouted(err) {
				return fmt.Errorf("the server at %s does not offer %s", cl.Endpoint(), c.CommandPath())
			}
			return err
		},
	}
	c.Flags().Bool("table", false, "render the answer as columns: a list as one table, a record as its fields then its lists")
	for _, f := range append(append([]Flag{}, op.Query...), op.Body...) {
		if f.Kind == "files" {
			// Repeated (--files a --files b) or comma-separated, one part each.
			c.Flags().StringSlice(f.Name, nil, strings.TrimSpace(f.Doc+" (repeat the flag, or separate with commas)"))
		} else {
			c.Flags().String(f.Name, "", f.Doc)
		}
		if f.Required {
			c.MarkFlagRequired(f.Name)
		}
	}
	return c
}

// unrouted is whether the server answered 404 with no refusal of its own: no route, rather than a record that is not
// there (whose refusal names its code).
func unrouted(err error) bool {
	var failure *client.Failure
	return errors.As(err, &failure) && failure.Status == 404 && failure.Code == ""
}

// call turns the flags into one request and prints the answer.
func call(api *client.Client, op Operation, args []string, flags *pflag.FlagSet, table bool, out io.Writer) error {
	path := op.Path
	for i, name := range op.Params {
		path = strings.Replace(path, "{"+name+"}", url.PathEscape(args[i]), 1)
	}
	query, form, body := url.Values{}, url.Values{}, map[string]any{}
	files := map[string][]string{}
	for _, f := range op.Query {
		if flags.Changed(f.Name) {
			query.Set(f.Name, flags.Lookup(f.Name).Value.String())
		}
	}
	for _, f := range op.Body {
		if !flags.Changed(f.Name) {
			continue
		}
		raw := flags.Lookup(f.Name).Value.String()
		switch f.Kind {
		case "file":
			files[f.Name] = []string{raw}
		case "files":
			paths, _ := flags.GetStringSlice(f.Name)
			for _, v := range paths {
				files[f.Name+"[]"] = append(files[f.Name+"[]"], strings.TrimSpace(v))
			}
		case "array":
			if strings.HasPrefix(strings.TrimSpace(raw), "[") {
				var v []any
				if err := json.Unmarshal([]byte(raw), &v); err != nil {
					return fmt.Errorf("--%s: expected a JSON list: %w", f.Name, err)
				}
				body[f.Name] = v
				continue
			}
			for _, v := range strings.Split(raw, ",") {
				form.Add(f.Name+"[]", strings.TrimSpace(v))
			}
			body[f.Name] = strings.Split(raw, ",")
		default:
			v, err := value(f.Kind, raw)
			if err != nil {
				return fmt.Errorf("--%s: %w", f.Name, err)
			}
			form.Set(f.Name, raw)
			body[f.Name] = v
		}
	}

	var status int
	var answer json.RawMessage
	var err error
	switch {
	case op.Multipart:
		status, answer, err = api.Multipart(op.Method, path, form, files)
	case len(body) > 0:
		status, answer, err = api.JSON(op.Method, path, query, body)
	default:
		status, answer, err = api.JSON(op.Method, path, query, nil)
	}
	if err != nil {
		return err
	}
	if status == 204 || len(answer) == 0 {
		return nil
	}
	// A download (a version's file) is written as it came.
	if !json.Valid(answer) {
		_, err := out.Write(answer)
		return err
	}
	if table {
		return printTable(answer, op.Answer, out)
	}
	return printJSON(answer, out)
}

// An empty integer or number is null, which clears the field.
func value(kind, raw string) (any, error) {
	if raw == "" && (kind == "integer" || kind == "number") {
		return nil, nil
	}
	switch kind {
	case "boolean":
		return strconv.ParseBool(raw)
	case "integer":
		return strconv.Atoi(raw)
	case "number":
		return strconv.ParseFloat(raw, 64)
	case "object":
		var v map[string]any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("expected a JSON object: %w", err)
		}
		return v, nil
	}
	return raw, nil
}

func printJSON(raw json.RawMessage, out io.Writer) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("the answer is not JSON")
	}
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(out, string(data))
	return nil
}

// A list in the envelope prints as one table. A single record prints its scalar fields as name and
// value rows, then each list of records it holds as a table under the list's name. Columns are the
// scalar keys, `id` and `name` first, the rest sorted. When the document declares the answer, its
// scalar fields are columns whether a value is there or not, and a field it declares as an object
// never is: the columns of a command stay the same from one call to the next. A list that came back
// empty prints its columns alone when nothing else in the envelope has a row.
func printTable(raw json.RawMessage, answer *schema, out io.Writer) error {
	var envelope map[string]json.RawMessage
	json.Unmarshal(raw, &envelope)
	keys := sortedKeys(envelope)
	for _, k := range keys {
		var rows []map[string]any
		if json.Unmarshal(envelope[k], &rows) == nil && len(rows) > 0 {
			return writeRows(rows, items(field(answer, k)), out)
		}
	}
	for _, k := range keys {
		var record map[string]any
		if json.Unmarshal(envelope[k], &record) == nil && len(record) > 0 {
			return writeRecord(record, field(answer, k), out)
		}
	}
	for _, k := range keys {
		var rows []map[string]any
		if json.Unmarshal(envelope[k], &rows) == nil && rows != nil && len(declared(items(field(answer, k)))) > 0 {
			return writeRows(rows, items(field(answer, k)), out)
		}
	}
	return printJSON(raw, out)
}

func writeRecord(record map[string]any, declaredAs *schema, out io.Writer) error {
	fields := map[string]bool{}
	for name := range declared(declaredAs) {
		fields[name] = true
	}
	var lists []string
	for name, v := range record {
		if list, ok := v.([]any); ok && len(list) == 0 {
			continue
		}
		if scalar(v) && !object(declaredAs, name) {
			fields[name] = true
		} else if len(records(v)) > 0 {
			lists = append(lists, name)
		}
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, name := range ordered(fields) {
		fmt.Fprintf(w, "%s\t%s\n", name, cell(record[name]))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	sort.Strings(lists)
	for _, name := range lists {
		fmt.Fprintf(out, "\n%s\n", name)
		if err := writeRows(records(record[name]), items(field(declaredAs, name)), out); err != nil {
			return err
		}
	}
	return nil
}

func writeRows(rows []map[string]any, item *schema, out io.Writer) error {
	cols := map[string]bool{}
	for name := range declared(item) {
		cols[name] = true
	}
	for _, row := range rows {
		for name, v := range row {
			if scalar(v) && !object(item, name) {
				cols[name] = true
			}
		}
	}
	order := ordered(cols)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(order, "\t"))
	for _, row := range rows {
		cells := make([]string, len(order))
		for i, col := range order {
			cells[i] = cell(row[col])
		}
		fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	return w.Flush()
}

// The declared field of an object schema, or nil.
func field(s *schema, name string) *schema {
	if s == nil {
		return nil
	}
	f, ok := s.Properties[name]
	if !ok {
		return nil
	}
	return &f
}

// The items of a declared list, or nil.
func items(s *schema) *schema {
	if s == nil || s.Type != "array" {
		return nil
	}
	return s.Items
}

// The fields a schema declares as values that fit a cell: a string, a number, a boolean, or a list of them.
func declared(s *schema) map[string]bool {
	fields := map[string]bool{}
	if s == nil {
		return fields
	}
	for name, f := range s.Properties {
		switch f.Type {
		case "string", "integer", "number", "boolean":
			fields[name] = true
		case "array":
			if f.Items != nil && f.Items.Type != "object" && f.Items.Type != "array" && f.Items.Type != "" {
				fields[name] = true
			}
		}
	}
	return fields
}

// Whether the schema declares the field as an object or a list of objects: never a column, even when null.
func object(s *schema, name string) bool {
	f := field(s, name)
	if f == nil {
		return false
	}
	return f.Type == "object" || (f.Type == "array" && f.Items != nil && f.Items.Type == "object")
}

// `id` and `name` first, the rest sorted.
func ordered(set map[string]bool) []string {
	var order []string
	for _, first := range []string{"id", "name"} {
		if set[first] {
			order = append(order, first)
		}
	}
	var rest []string
	for name := range set {
		if name != "id" && name != "name" {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(order, rest...)
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// A value that fits a cell: anything but an object or a list holding one.
func scalar(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		return false
	case []any:
		for _, item := range x {
			if _, ok := item.(map[string]any); ok {
				return false
			}
		}
	}
	return true
}

// The objects of a list, or nil when it holds anything else.
func records(v any) []map[string]any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	rows := make([]map[string]any, 0, len(list))
	for _, item := range list {
		row, ok := item.(map[string]any)
		if !ok {
			return nil
		}
		rows = append(rows, row)
	}
	return rows
}

func cell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []any:
		cells := make([]string, len(x))
		for i, item := range x {
			cells[i] = cell(item)
		}
		return strings.Join(cells, "; ")
	}
	return fmt.Sprint(v)
}

// Named is how a path argument reads in a usage line: an `{id}` after `/sessions` is a session-id, after `/repos` a
// repo-id; any other argument keeps its name, dashed.
func Named(path, param string) string {
	if param == "id" {
		segments := strings.Split(strings.Trim(path, "/"), "/")
		for i, s := range segments {
			if s == "{id}" && i > 0 {
				return strings.TrimSuffix(segments[i-1], "s") + "-id"
			}
		}
	}
	return strings.ReplaceAll(param, "_", "-")
}
