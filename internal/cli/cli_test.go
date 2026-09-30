package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mggarofalo/paperless-cli/internal/api"
)

func setup(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	t.Setenv("PAPERLESS_URL", s.URL)
	t.Setenv("PAPERLESS_API_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return s
}
func run(args ...string) (string, string, error) { return runInput("", args...) }
func runInput(input string, args ...string) (string, string, error) {
	c := New("test")
	var out, errOut bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetIn(strings.NewReader(input))
	c.SetArgs(args)
	err := c.Execute()
	return out.String(), errOut.String(), err
}

func TestPatchPreservesUnspecifiedFields(t *testing.T) {
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/api/documents/42/" {
			t.Errorf("%s %s", r.Method, r.URL)
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"title":"New title"}` {
			t.Error(string(b))
		}
		fmt.Fprint(w, `{"id":42,"title":"New title","tags":[1,2]}`)
	})
	out, stderr, err := run("document", "update", "42", "--title", "New title")
	if err != nil || stderr != "" || !json.Valid([]byte(out)) {
		t.Fatalf("%s %s %v", out, stderr, err)
	}
}

func TestDryRunNeverMutates(t *testing.T) {
	calls := 0
	setup(t, func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("unexpected request") })
	for _, args := range [][]string{{"document", "update", "42", "--tags", "1,2", "--dry-run"}, {"document", "delete", "42", "--dry-run"}, {"document", "bulk", "--documents", "1,2", "--method", "modify_tags", "--json", `{"add_tags":[3],"remove_tags":[]}`, "--dry-run"}} {
		out, _, err := run(args...)
		if err != nil || !json.Valid([]byte(out)) {
			t.Fatalf("%s %v", out, err)
		}
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}

func TestNameResolution(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			setup(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Fatal("mutation during dry-run")
				}
				items := []any{}
				for i := 0; i < count; i++ {
					items = append(items, map[string]any{"id": i + 7, "name": "Finance"})
				}
				json.NewEncoder(w).Encode(map[string]any{"results": items, "next": nil})
			})
			out, _, err := run("document", "update", "42", "--tags", "finance", "--dry-run")
			if count == 1 {
				if err != nil || !strings.Contains(out, "7") {
					t.Fatalf("%s %v", out, err)
				}
			} else if err == nil || ExitCode(err) != 4 {
				t.Fatalf("expected resolution failure: %v", err)
			}
		})
	}
}

func TestSearchEncodingAndPagination(t *testing.T) {
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != "invoice & tax" || r.URL.Query().Get("tags__id__all") != "1,2" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `{"count":1,"next":null,"results":[{"id":42}]}`)
	})
	out, _, err := run("document", "search", "invoice & tax", "--filter", "tags__id__all=1,2", "--all")
	if err != nil || !strings.Contains(out, `"count": 1`) {
		t.Fatalf("%s %v", out, err)
	}
}

func TestJSONStdinAndValidation(t *testing.T) {
	out, _, err := runInput(`{"custom_fields":[{"field":1,"value":9007199254740993}]}`, "document", "update", "42", "--json", "-", "--dry-run")
	if err != nil || !strings.Contains(out, "9007199254740993") {
		t.Fatalf("%s %v", out, err)
	}
	for _, args := range [][]string{{"document", "update", "42"}, {"document", "delete", "42"}, {"document", "get", "../42"}, {"document", "update", "42", "--json", "null"}, {"document", "update", "42", "--json", "{} {}"}, {"document", "bulk", "--documents", "1,-2", "--method", "delete", "--yes"}, {"api", "DELETE", "documents/1/"}, {"document", "list", "--page-size", "0"}} {
		_, _, err := run(args...)
		if err == nil || ExitCode(err) != 4 {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestUploadAndDownload(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "input.pdf")
	os.WriteFile(source, []byte("%PDF-test"), 0600)
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			f, h, err := r.FormFile("document")
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			if string(b) != "%PDF-test" || h.Filename != "input.pdf" || len(r.MultipartForm.Value["tags"]) != 2 {
				t.Error("incorrect upload")
			}
			fmt.Fprint(w, `"task-uuid"`)
			return
		}
		fmt.Fprint(w, "%PDF-test")
	})
	out, _, err := run("document", "upload", source, "--field", "tags=1", "--field", "tags=2")
	if err != nil || strings.TrimSpace(out) != `"task-uuid"` {
		t.Fatalf("%s %v", out, err)
	}
	dest := filepath.Join(dir, "output.pdf")
	_, _, err = run("document", "download", "42", "--file", dest)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "%PDF-test" {
		t.Error(string(b))
	}
	_, _, err = run("document", "download", "42", "--file", dest)
	if err == nil {
		t.Fatal("overwrote existing file")
	}
}

func TestExitCodes(t *testing.T) {
	for status, want := range map[int]int{400: 4, 401: 2, 403: 2, 404: 3, 422: 4, 429: 5, 500: 1} {
		if got := ExitCode(&api.Error{Status: status}); got != want {
			t.Fatalf("%d: got %d want %d", status, got, want)
		}
	}
}

func TestClearRelationships(t *testing.T) {
	out, _, err := run("document", "update", "42", "--tags=", "--correspondent", "null", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var plan struct{ Body map[string]any }
	json.Unmarshal([]byte(out), &plan)
	if plan.Body["correspondent"] != nil || len(plan.Body["tags"].([]any)) != 0 {
		t.Fatal(out)
	}
}

func TestArgumentErrors(t *testing.T) {
	for _, args := range [][]string{{"document", "search"}, {"api", "GET"}, {"tag", "list", "extra"}} {
		_, _, err := run(args...)
		if err == nil || ExitCode(err) != 4 {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestConfigAndCredentialIsolation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config{Profiles: map[string]string{"default": "https://example.test"}}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig()
	if err != nil || got.Profiles["default"] != cfg.Profiles["default"] {
		t.Fatalf("%v %v", got, err)
	}
	if credentialKey("default", "https://a.test") == credentialKey("default", "https://b.test") {
		t.Fatal("credential collision across URLs")
	}
	if credentialKey("default", "https://a.test") == credentialKey("other", "https://a.test") {
		t.Fatal("credential collision across profiles")
	}
	t.Setenv("PAPERLESS_URL", "https://env.test")
	t.Setenv("PAPERLESS_API_TOKEN", "test-token")
	o := options{profile: "default", url: "https://flag.test"}
	base, token, err := o.credentials()
	if err != nil || base != o.url || token != "test-token" {
		t.Fatalf("credential precedence failed: %v", err)
	}
}

func TestDeleteOutput(t *testing.T) {
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Error(r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	out, stderr, err := run("document", "delete", "42", "--yes")
	if err != nil || out != "" || stderr != "Deleted.\n" {
		t.Fatalf("%q %q %v", out, stderr, err)
	}
}

func TestPartialDownloadCleanup(t *testing.T) {
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		fmt.Fprint(w, "short")
	})
	file := filepath.Join(t.TempDir(), "partial.pdf")
	_, _, err := run("document", "download", "42", "--file", file)
	if err == nil {
		t.Fatal("expected download failure")
	}
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("partial file was not removed")
	}
}
