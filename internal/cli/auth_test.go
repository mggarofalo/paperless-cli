package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/99designs/keyring"
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

type memoryCredentials map[string]keyring.Item

func (m memoryCredentials) Get(key string) (keyring.Item, error) {
	item, ok := m[key]
	if !ok {
		return keyring.Item{}, keyring.ErrKeyNotFound
	}
	return item, nil
}
func (m memoryCredentials) Set(item keyring.Item) error { m[item.Key] = item; return nil }
func (m memoryCredentials) Remove(key string) error {
	if _, ok := m[key]; !ok {
		return keyring.ErrKeyNotFound
	}
	delete(m, key)
	return nil
}

func TestLoginRetiresPreviousInstanceToken(t *testing.T) {
	oldKey := credentialKey("default", "https://old.example.com")
	otherKey := credentialKey("other", "https://old.example.com")
	store := memoryCredentials{oldKey: {Key: oldKey, Data: []byte("old-token")}, otherKey: {Key: otherKey, Data: []byte("other-token")}}
	cfg := config{Profiles: map[string]string{"default": "https://old.example.com", "other": "https://old.example.com"}}
	var saved config
	err := saveLoginCredentials(cfg, "default", "https://new.example.com", "new-token", store, func(c config) error { saved = c; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(oldKey); !errors.Is(err, keyring.ErrKeyNotFound) {
		t.Fatal("old token retained")
	}
	if _, err := store.Get(otherKey); err != nil {
		t.Fatal("another profile was affected")
	}
	if saved.Profiles["default"] != "https://new.example.com" || cfg.Profiles["default"] != "https://old.example.com" {
		t.Fatal("incorrect profile update")
	}
}

func TestLoginConfigFailureRestoresKeyring(t *testing.T) {
	for _, sameURL := range []bool{false, true} {
		t.Run(map[bool]string{false: "new URL", true: "same URL"}[sameURL], func(t *testing.T) {
			oldURL, newURL := "https://old.example.com", "https://new.example.com"
			if sameURL {
				newURL = oldURL
			}
			oldKey := credentialKey("default", oldURL)
			store := memoryCredentials{oldKey: {Key: oldKey, Data: []byte("working-token")}}
			cfg := config{Profiles: map[string]string{"default": oldURL}}
			failure := errors.New("disk full")
			err := saveLoginCredentials(cfg, "default", newURL, "replacement-token", store, func(config) error { return failure })
			if !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if len(store) != 1 || string(store[oldKey].Data) != "working-token" || cfg.Profiles["default"] != oldURL {
				t.Fatal("failed login modified working credentials")
			}
		})
	}
}

func TestConfigReplacement(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, base := range []string{"https://old.example.com", "https://new.example.com"} {
		if err := saveConfig(config{Profiles: map[string]string{"default": base}}); err != nil {
			t.Fatal(err)
		}
		got, err := loadConfig()
		if err != nil || got.Profiles["default"] != base {
			t.Fatalf("config not replaced: %v", err)
		}
	}
}

type failingCredentials struct {
	memoryCredentials
	failure error
}

func (s failingCredentials) Get(string) (keyring.Item, error) { return keyring.Item{}, s.failure }

func TestCredentialTokenErrors(t *testing.T) {
	failure := errors.New("keyring locked")
	for _, op := range []string{"open", "get"} {
		t.Run(op, func(t *testing.T) {
			_, err := credentialToken("default", "https://example.com", "", func() (credentialStore, error) {
				if op == "open" {
					return nil, failure
				}
				return failingCredentials{failure: failure}, nil
			})
			if !errors.Is(err, failure) {
				t.Fatalf("lost keyring failure: %v", err)
			}
		})
	}
	token, err := credentialToken("default", "https://example.com", "", func() (credentialStore, error) { return memoryCredentials{}, nil })
	if err != nil || token != "" {
		t.Fatalf("missing credential should not be a backend failure: %v", err)
	}
	for _, tc := range []struct{ base, token string }{{"https://example.com", "env-token"}, {"", ""}} {
		token, err := credentialToken("default", tc.base, tc.token, func() (credentialStore, error) { t.Fatal("unexpected keyring access"); return nil, nil })
		if err != nil || token != tc.token {
			t.Fatal("environment/no-config handling changed")
		}
	}
}

func TestLogoutCredentials(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "config failure"}[fail], func(t *testing.T) {
			key := credentialKey("default", "https://example.com")
			store := memoryCredentials{key: {Key: key, Data: []byte("working-token")}}
			cfg := config{Profiles: map[string]string{"default": "https://example.com", "other": "https://other.example.com"}}
			failure := errors.New("disk full")
			err := logoutCredentials(cfg, "default", store, func(c config) error {
				if c.Profiles["default"] != "" || c.Profiles["other"] == "" {
					t.Fatal("incorrect logout config")
				}
				if fail {
					return failure
				}
				return nil
			})
			if fail {
				if !errors.Is(err, failure) || string(store[key].Data) != "working-token" {
					t.Fatal("logout failed to restore token")
				}
			} else if err != nil || len(store) != 0 {
				t.Fatal("logout failed")
			}
			if cfg.Profiles["default"] == "" {
				t.Fatal("input config mutated")
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
