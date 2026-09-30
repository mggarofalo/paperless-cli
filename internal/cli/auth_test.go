package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestInteractiveLoginInputs(t *testing.T) {
	for _, tc := range []struct{ name, base, input, want string }{
		{"first login", "", "paperless.example.com\n", "https://paperless.example.com"},
		{"accept saved URL", "https://saved.example.com", "\n", "https://saved.example.com"},
		{"change saved URL", "https://saved.example.com", "https://new.example.com/subpath/\n", "https://new.example.com/subpath/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			called := false
			base, token, err := loginInput(strings.NewReader(tc.input), &out, tc.base, "", true, false, func() ([]byte, error) { called = true; return []byte("secret-token"), nil })
			if err != nil || base != tc.want || token != "secret-token" || !called {
				t.Fatalf("incorrect login inputs or prompt: %v", err)
			}
			if !strings.Contains(out.String(), "Paperless URL") || !strings.Contains(out.String(), "API token (hidden)") || strings.Contains(out.String(), "secret-token") {
				t.Fatalf("bad prompts: %q", out.String())
			}
		})
	}
}

func TestLoginTokenStdin(t *testing.T) {
	var out bytes.Buffer
	base, token, err := loginInput(strings.NewReader("secret-token\n"), &out, "https://example.com", "", false, true, nil)
	if err != nil || base != "https://example.com" || token != "secret-token" || out.Len() != 0 {
		t.Fatalf("stdin login failed: %v", err)
	}
}

func TestLoginInputErrors(t *testing.T) {
	for _, tc := range []struct {
		name, base, input  string
		interactive, stdin bool
	}{
		{"missing URL", "", "", false, true},
		{"missing token", "https://example.com", "", false, false},
		{"empty token", "https://example.com", "", false, true},
		{"invalid URL", "ftp://example.com", "", true, false},
		{"cancelled URL", "", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var password func() ([]byte, error)
			if tc.interactive {
				password = func() ([]byte, error) { t.Error("unexpected password prompt"); return nil, nil }
			}
			_, _, err := loginInput(strings.NewReader(tc.input), io.Discard, tc.base, "", tc.base == "", tc.stdin, password)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
	_, _, err := loginInput(strings.NewReader(""), io.Discard, "https://example.com", "", false, false, func() ([]byte, error) { return nil, io.EOF })
	if !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestURLPromptDoesNotConsumeToken(t *testing.T) {
	in := strings.NewReader("https://example.com\nsecret-token\n")
	if _, err := readPromptLine(in); err != nil {
		t.Fatal(err)
	}
	remaining, _ := io.ReadAll(in)
	if string(remaining) != "secret-token\n" {
		t.Fatal("URL prompt consumed token input")
	}
}
