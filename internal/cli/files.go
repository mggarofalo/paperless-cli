package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func uploadCommand(o *options) *cobra.Command {
	var fields []string
	cmd := &cobra.Command{Use: "upload FILE", Short: "Upload a document; returns a consumption task UUID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		f, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return invalid("upload requires a regular file")
		}
		values := map[string][]string{}
		for _, s := range fields {
			k, v, ok := strings.Cut(s, "=")
			if !ok || k == "" || k == "document" {
				return invalid("field must be key=value and cannot replace document")
			}
			values[k] = append(values[k], v)
		}
		if o.dryRun {
			return write(cmd, map[string]any{"method": "POST", "path": "documents/post_document/", "file": args[0], "fields": values}, "json")
		}
		c, err := o.client()
		if err != nil {
			return err
		}
		// Spool multipart data to disk for bounded memory and no background goroutines.
		body, err := os.CreateTemp("", "paperless-upload-*")
		if err != nil {
			return err
		}
		defer os.Remove(body.Name())
		defer body.Close()
		w := multipart.NewWriter(body)
		part, err := w.CreateFormFile("document", filepath.Base(args[0]))
		if err != nil {
			return err
		}
		if _, err = io.Copy(part, f); err != nil {
			return err
		}
		for k, vs := range values {
			for _, v := range vs {
				if err = w.WriteField(k, v); err != nil {
					return err
				}
			}
		}
		if err = w.Close(); err != nil {
			return err
		}
		if _, err = body.Seek(0, 0); err != nil {
			return err
		}
		resp, err := c.Request(cmd.Context(), "POST", "documents/post_document/", body, w.FormDataContentType())
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		var v any
		if err = json.NewDecoder(resp.Body).Decode(&v); err != nil {
			return err
		}
		return write(cmd, v, o.output)
	}}
	cmd.Flags().StringArrayVar(&fields, "field", nil, "Multipart metadata key=value; numeric relationship IDs; repeat tags=ID")
	return cmd
}

func downloadCommand(o *options) *cobra.Command {
	var file string
	var original bool
	cmd := &cobra.Command{Use: "download ID --file PATH", Short: "Download document to a new file", Args: idArg, RunE: func(cmd *cobra.Command, args []string) error {
		if file == "" {
			return invalid("--file is required")
		}
		if o.dryRun {
			return invalid("download does not support --dry-run")
		}
		c, err := o.client()
		if err != nil {
			return err
		}
		path := "documents/" + args[0] + "/download/"
		if original {
			path += "?original=true"
		}
		resp, err := c.Request(cmd.Context(), "GET", path, nil, "")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		complete := false
		defer func() {
			f.Close()
			if !complete {
				os.Remove(file)
			}
		}()
		n, err := io.Copy(f, resp.Body)
		if err != nil {
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		complete = true
		fmt.Fprintf(cmd.ErrOrStderr(), "Downloaded %d bytes.\n", n)
		return write(cmd, map[string]any{"id": args[0], "file": file, "bytes": n}, o.output)
	}}
	cmd.Flags().StringVar(&file, "file", "", "Destination path (must not already exist)")
	cmd.Flags().BoolVar(&original, "original", false, "Download original instead of archived version")
	return cmd
}
