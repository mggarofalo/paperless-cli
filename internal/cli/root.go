package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mggarofalo/paperless-cli/internal/api"
	"github.com/spf13/cobra"
)

type options struct {
	url, profile, output string
	timeout              time.Duration
	dryRun, yes          bool
}
type validationError struct{ error }

func invalid(format string, args ...any) error { return &validationError{fmt.Errorf(format, args...)} }
func ExitCode(err error) int {
	var v *validationError
	if errors.As(err, &v) {
		return 4
	}
	var e *api.Error
	if errors.As(err, &e) {
		switch e.Status {
		case 401, 403:
			return 2
		case 404:
			return 3
		case 400, 422:
			return 4
		case 429:
			return 5
		}
	}
	return 1
}

func New(version string) *cobra.Command {
	o := &options{}
	r := &cobra.Command{Use: "paperless", Short: "Organize Paperless-ngx documents from a terminal or agent", Version: version, SilenceUsage: true, SilenceErrors: true}
	r.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return invalid("%v", err) })
	r.PersistentFlags().StringVar(&o.url, "url", "", "Instance URL (PAPERLESS_URL)")
	r.PersistentFlags().StringVar(&o.profile, "profile", envDefault("PAPERLESS_PROFILE", "default"), "Credential profile")
	r.PersistentFlags().StringVarP(&o.output, "output", "o", "json", "Output: json or table")
	r.PersistentFlags().DurationVar(&o.timeout, "timeout", 60*time.Second, "HTTP request timeout")
	r.PersistentFlags().BoolVar(&o.dryRun, "dry-run", false, "Print mutation request without sending it (name resolution may read API)")
	r.PersistentFlags().BoolVar(&o.yes, "yes", false, "Confirm destructive operations")
	r.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if o.output != "json" && o.output != "table" {
			return invalid("output must be json or table")
		}
		return nil
	}
	r.AddCommand(authCommand(o))
	for _, res := range resources {
		r.AddCommand(resourceCommand(o, res))
	}
	r.AddCommand(rawCommand(o), schemaCommand(o))
	wrapArgumentErrors(r)
	return r
}

func wrapArgumentErrors(cmd *cobra.Command) {
	if check := cmd.Args; check != nil {
		cmd.Args = func(cmd *cobra.Command, args []string) error {
			if err := check(cmd, args); err != nil {
				return invalid("%v", err)
			}
			return nil
		}
	}
	for _, child := range cmd.Commands() {
		wrapArgumentErrors(child)
	}
}

func envDefault(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
func (o *options) client() (*api.Client, error) {
	base, token, err := o.credentials()
	if err != nil {
		return nil, err
	}
	return api.New(base, token, o.timeout)
}
func write(cmd *cobra.Command, v any, format string) error {
	if v == nil {
		return nil
	}
	if format == "json" {
		e := json.NewEncoder(cmd.OutOrStdout())
		e.SetIndent("", "  ")
		return e.Encode(v)
	}
	items, ok := v.([]any)
	if obj, yes := v.(map[string]any); yes {
		items, ok = obj["results"].([]any)
		if !ok {
			items = []any{obj}
			ok = true
		}
	}
	if !ok {
		return write(cmd, v, "json")
	}
	cols := []string{}
	for _, key := range []string{"id", "name", "title", "created", "correspondent", "document_type", "tags", "status", "task_id"} {
		for _, v := range items {
			if obj, ok := v.(map[string]any); ok {
				if _, found := obj[key]; found {
					cols = append(cols, key)
					break
				}
			}
		}
	}
	if len(cols) == 0 {
		return write(cmd, v, "json")
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(cols, "\t"))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return invalid("table requires objects")
		}
		cells := []string{}
		for _, k := range cols {
			s := ""
			if obj[k] != nil {
				s = fmt.Sprint(obj[k])
			}
			s = strings.Map(func(r rune) rune {
				if r < 32 || r == 127 {
					return ' '
				}
				return r
			}, s)
			cells = append(cells, s)
		}
		fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	return w.Flush()
}

func readObject(cmd *cobra.Command, s string) (map[string]any, error) {
	if s == "" {
		return map[string]any{}, nil
	}
	var b []byte
	var err error
	if s == "-" {
		b, err = io.ReadAll(cmd.InOrStdin())
	} else if strings.HasPrefix(s, "@") {
		b, err = os.ReadFile(strings.TrimPrefix(s, "@"))
	} else {
		b = []byte(s)
	}
	if err != nil {
		return nil, err
	}
	var v map[string]any
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if err = d.Decode(&v); err != nil || v == nil {
		return nil, invalid("--json requires a JSON object")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, invalid("unexpected data after JSON object")
	}
	return v, nil
}

func (o *options) send(cmd *cobra.Command, method, path string, body any) error {
	if o.dryRun && method != "GET" && method != "OPTIONS" {
		return write(cmd, map[string]any{"method": method, "path": path, "body": body}, "json")
	}
	c, err := o.client()
	if err != nil {
		return err
	}
	v, err := c.JSON(cmd.Context(), method, path, body)
	if err != nil {
		return err
	}
	if method == "DELETE" {
		fmt.Fprintln(cmd.ErrOrStderr(), "Deleted.")
	}
	return write(cmd, v, o.output)
}
