package cli

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type resource struct {
	name, path string
	writable   bool
}

var resources = []resource{
	{"document", "documents", true}, {"tag", "tags", true}, {"correspondent", "correspondents", true},
	{"document-type", "document_types", true}, {"storage-path", "storage_paths", true},
	{"custom-field", "custom_fields", true}, {"saved-view", "saved_views", true},
	{"task", "tasks", false}, {"workflow", "workflows", true},
}

func id(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, invalid("expected positive numeric ID, got %q", s)
	}
	return n, nil
}
func idArg(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return invalid("requires one ID")
	}
	_, err := id(args[0])
	return err
}

func resourceCommand(o *options, res resource) *cobra.Command {
	r := &cobra.Command{Use: res.name, Aliases: []string{res.path}, Short: "Manage " + strings.ReplaceAll(res.path, "_", " ")}
	if res.name == res.path {
		r.Aliases = nil
	}
	r.AddCommand(listCommand(o, res, false))
	get := &cobra.Command{Use: "get ID", Short: "Get by ID", Args: idArg, RunE: func(cmd *cobra.Command, args []string) error {
		return o.send(cmd, "GET", res.path+"/"+args[0]+"/", nil)
	}}
	// Tasks are queried by task UUID using list --filter task_id=UUID.
	if res.name != "task" {
		r.AddCommand(get)
	}
	if res.writable {
		if res.name != "document" {
			r.AddCommand(editCommand(o, res, false))
		}
		r.AddCommand(editCommand(o, res, true))
		r.AddCommand(&cobra.Command{Use: "delete ID", Short: "Delete a resource (requires --yes)", Args: idArg, RunE: func(cmd *cobra.Command, args []string) error {
			if !o.yes && !o.dryRun {
				return invalid("delete requires --yes (or preview with --dry-run)")
			}
			return o.send(cmd, "DELETE", res.path+"/"+args[0]+"/", nil)
		}})
	}
	if res.name == "document" {
		r.AddCommand(listCommand(o, res, true), bulkCommand(o), uploadCommand(o), downloadCommand(o))
		for _, action := range []string{"content", "metadata"} {
			action := action
			r.AddCommand(&cobra.Command{Use: action + " ID", Short: "Read document " + action, Args: idArg, RunE: func(cmd *cobra.Command, args []string) error {
				path := "documents/" + args[0] + "/"
				if action == "metadata" {
					return o.send(cmd, "GET", path+"metadata/", nil)
				}
				c, err := o.client()
				if err != nil {
					return err
				}
				v, err := c.JSON(cmd.Context(), "GET", path, nil)
				if err != nil {
					return err
				}
				obj, ok := v.(map[string]any)
				if !ok {
					return fmt.Errorf("expected document object")
				}
				return write(cmd, map[string]any{"id": obj["id"], "title": obj["title"], "content": obj["content"]}, o.output)
			}})
		}
	}
	return r
}

func listCommand(o *options, res resource, search bool) *cobra.Command {
	var all bool
	var page, size int
	var filters []string
	var ordering, query string
	cmd := &cobra.Command{Use: "list", Short: "List resources with API filters", Args: cobra.NoArgs}
	if search {
		cmd.Use = "search QUERY"
		cmd.Short = "Full-text document search"
		cmd.Args = cobra.ExactArgs(1)
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if page < 1 || size < 1 || size > 1000 {
			return invalid("page must be positive; page-size must be 1..1000")
		}
		q := url.Values{"page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(size)}}
		for _, s := range filters {
			k, v, ok := strings.Cut(s, "=")
			if !ok || k == "" {
				return invalid("filter must be key=value")
			}
			q.Add(k, v)
		}
		if ordering != "" {
			q.Set("ordering", ordering)
		}
		if search {
			query = args[0]
		}
		if query != "" {
			q.Set("query", query)
		}
		c, err := o.client()
		if err != nil {
			return err
		}
		v, err := c.List(cmd.Context(), res.path+"/?"+q.Encode(), all)
		if err != nil {
			return err
		}
		return write(cmd, v, o.output)
	}
	cmd.Flags().BoolVar(&all, "all", false, "Fetch every page; return {count, results}")
	cmd.Flags().IntVar(&page, "page", 1, "Page number")
	cmd.Flags().IntVar(&size, "page-size", 100, "Results per page")
	cmd.Flags().StringArrayVarP(&filters, "filter", "f", nil, "API query parameter key=value (repeatable)")
	cmd.Flags().StringVar(&ordering, "ordering", "", "API ordering, e.g. -created")
	if res.name == "document" && !search {
		cmd.Flags().StringVar(&query, "query", "", "Full-text query")
	}
	return cmd
}

func (o *options) resolve(cmd *cobra.Command, path, value string) (any, error) {
	if value == "null" {
		return nil, nil
	}
	if n, err := strconv.Atoi(value); err == nil {
		if n <= 0 {
			return nil, invalid("ID must be positive")
		}
		return n, nil
	}
	c, err := o.client()
	if err != nil {
		return nil, err
	}
	v, err := c.List(cmd.Context(), path+"/?page_size=100", true)
	if err != nil {
		return nil, err
	}
	var match any
	count := 0
	for _, item := range v.(map[string]any)["results"].([]any) {
		if obj, ok := item.(map[string]any); ok {
			if name, ok := obj["name"].(string); ok && strings.EqualFold(name, value) {
				match = obj["id"]
				count++
			}
		}
	}
	if count != 1 {
		return nil, invalid("%q matches %d %s; use an exact unique name or numeric ID", value, count, path)
	}
	return match, nil
}

func editCommand(o *options, res resource, update bool) *cobra.Command {
	var input, name, title, correspondent, docType, storage, created string
	var tags []string
	cmd := &cobra.Command{Use: "create", Short: "Create using --name and/or --json", Args: cobra.NoArgs}
	if update {
		cmd.Use = "update ID"
		cmd.Short = "Patch only the supplied fields"
		cmd.Args = idArg
	}
	cmd.Flags().StringVar(&input, "json", "", "JSON object, @file, or - for stdin (API fields)")
	if res.name != "document" {
		cmd.Flags().StringVar(&name, "name", "", "Resource name")
	}
	if res.name == "document" {
		cmd.Flags().StringVar(&title, "title", "", "Document title")
		cmd.Flags().StringVar(&correspondent, "correspondent", "", "Correspondent name, ID, or null")
		cmd.Flags().StringVar(&docType, "document-type", "", "Document type name, ID, or null")
		cmd.Flags().StringVar(&storage, "storage-path", "", "Storage path name, ID, or null")
		cmd.Flags().StringSliceVar(&tags, "tags", nil, "Replace all tags with names/IDs (comma-separated); empty clears")
		cmd.Flags().StringVar(&created, "created", "", "Document date (ISO 8601)")
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		body, err := readObject(cmd, input)
		if err != nil {
			return err
		}
		for flag, val := range map[string]string{"name": name, "title": title, "created": created} {
			if cmd.Flags().Changed(flag) {
				body[flag] = val
			}
		}
		for _, field := range []struct{ flag, path, key, value string }{{"correspondent", "correspondents", "correspondent", correspondent}, {"document-type", "document_types", "document_type", docType}, {"storage-path", "storage_paths", "storage_path", storage}} {
			if cmd.Flags().Changed(field.flag) {
				v, err := o.resolve(cmd, field.path, field.value)
				if err != nil {
					return err
				}
				body[field.key] = v
			}
		}
		if cmd.Flags().Changed("tags") {
			ids := []any{}
			for _, tag := range tags {
				if tag == "null" {
					return invalid("tag cannot be null")
				}
				v, err := o.resolve(cmd, "tags", tag)
				if err != nil {
					return err
				}
				ids = append(ids, v)
			}
			body["tags"] = ids
		}
		if len(body) == 0 {
			return invalid("supply at least one field or --json")
		}
		method, path := "POST", res.path+"/"
		if update {
			method = "PATCH"
			path += args[0] + "/"
		}
		return o.send(cmd, method, path, body)
	}
	return cmd
}

func bulkCommand(o *options) *cobra.Command {
	var docs []int
	var method, input string
	cmd := &cobra.Command{Use: "bulk", Short: "Submit a bulk operation on explicit document IDs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if len(docs) == 0 {
			return invalid("--documents is required")
		}
		for _, n := range docs {
			if n <= 0 {
				return invalid("document IDs must be positive")
			}
		}
		allowed := map[string]bool{"set_correspondent": true, "set_document_type": true, "set_storage_path": true, "add_tag": true, "remove_tag": true, "modify_tags": true, "modify_custom_fields": true, "set_permissions": true, "reprocess": true, "delete": true}
		if !allowed[method] {
			return invalid("unsupported bulk method; see --help")
		}
		if method == "delete" && !o.yes && !o.dryRun {
			return invalid("bulk delete requires --yes")
		}
		body, err := readObject(cmd, input)
		if err != nil {
			return err
		}
		return o.send(cmd, "POST", "documents/bulk_edit/", map[string]any{"documents": docs, "method": method, "parameters": body})
	}}
	cmd.Flags().IntSliceVar(&docs, "documents", nil, "Explicit document IDs, comma-separated")
	cmd.Flags().StringVar(&method, "method", "", "set_correspondent, set_document_type, set_storage_path, add_tag, remove_tag, modify_tags, modify_custom_fields, set_permissions, reprocess, delete")
	cmd.Flags().StringVar(&input, "json", "{}", "Parameters object, @file, or -; relationship values are numeric IDs")
	return cmd
}

func rawCommand(o *options) *cobra.Command {
	var input string
	var filters []string
	cmd := &cobra.Command{Use: "api METHOD PATH", Short: "Call an API-relative endpoint (escape hatch)", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		method := strings.ToUpper(args[0])
		switch method {
		case "GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS":
		default:
			return invalid("unsupported HTTP method")
		}
		if method == "DELETE" && !o.yes && !o.dryRun {
			return invalid("DELETE requires --yes")
		}
		u, err := url.Parse(args[1])
		if err != nil {
			return err
		}
		if u.IsAbs() || u.Host != "" || strings.HasPrefix(args[1], "/") {
			return invalid("PATH must be API-relative, e.g. documents/42/")
		}
		q := u.Query()
		for _, s := range filters {
			k, v, ok := strings.Cut(s, "=")
			if !ok || k == "" {
				return invalid("filter must be key=value")
			}
			q.Add(k, v)
		}
		u.RawQuery = q.Encode()
		var body any
		if input != "" {
			body, err = readObject(cmd, input)
			if err != nil {
				return err
			}
		}
		return o.send(cmd, method, u.String(), body)
	}}
	cmd.Flags().StringVar(&input, "json", "", "JSON object, @file, or -")
	cmd.Flags().StringArrayVarP(&filters, "filter", "f", nil, "Query key=value")
	return cmd
}
func schemaCommand(o *options) *cobra.Command {
	return &cobra.Command{Use: "schema", Short: "Fetch this instance's OpenAPI schema as JSON", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error { return o.send(cmd, "GET", "schema/?format=json", nil) }}
}
