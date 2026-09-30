package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/99designs/keyring"
	"github.com/mggarofalo/paperless-cli/internal/api"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type config struct {
	Profiles map[string]string `json:"profiles"`
}

func configPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		var err error
		dir, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, "paperless-cli", "config.json"), nil
}
func loadConfig() (config, error) {
	c := config{Profiles: map[string]string{}}
	p, err := configPath()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	if c.Profiles == nil {
		c.Profiles = map[string]string{}
	}
	return c, err
}
func saveConfig(c config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0600)
}
func openRing() (keyring.Keyring, error) {
	return keyring.Open(keyring.Config{ServiceName: "paperless-cli", AllowedBackends: []keyring.BackendType{keyring.WinCredBackend, keyring.KeychainBackend, keyring.SecretServiceBackend, keyring.KWalletBackend}})
}

// Key credentials by instance as well as profile; URL overrides never reuse another instance's token.
func credentialKey(profile, base string) string {
	return profile + "/" + strings.TrimRight(base, "/") + "/token"
}
func (o *options) credentials() (string, string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return "", "", err
	}
	base := o.url
	if base == "" {
		base = os.Getenv("PAPERLESS_URL")
	}
	if base == "" {
		base = cfg.Profiles[o.profile]
	}
	token := os.Getenv("PAPERLESS_API_TOKEN")
	if token == "" {
		if ring, e := openRing(); e == nil {
			if item, e := ring.Get(credentialKey(o.profile, base)); e == nil {
				token = string(item.Data)
			}
		}
	}
	return base, token, nil
}
func authCommand(o *options) *cobra.Command {
	r := &cobra.Command{Use: "auth", Short: "Manage OS-keyring credentials"}
	var stdin bool
	login := &cobra.Command{Use: "login", Short: "Prompt for instance URL and API token, then save credentials", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if o.dryRun {
			return invalid("auth login does not support --dry-run")
		}
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		base := o.url
		if base == "" {
			base = os.Getenv("PAPERLESS_URL")
		}
		askURL := base == ""
		if base == "" {
			base = cfg.Profiles[o.profile]
		}
		var password func() ([]byte, error)
		if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
			password = func() ([]byte, error) { return term.ReadPassword(int(f.Fd())) }
		}
		base, token, err := loginInput(cmd.InOrStdin(), cmd.ErrOrStderr(), base, os.Getenv("PAPERLESS_API_TOKEN"), askURL, stdin, password)
		if err != nil {
			return err
		}
		c, err := api.New(base, token, o.timeout)
		if err != nil {
			return err
		}
		if _, err = c.JSON(cmd.Context(), "GET", "documents/?page_size=1", nil); err != nil {
			return err
		}
		ring, err := openRing()
		if err != nil {
			return fmt.Errorf("OS keyring unavailable; use PAPERLESS_API_TOKEN: %w", err)
		}
		if err = ring.Set(keyring.Item{Key: credentialKey(o.profile, base), Data: []byte(token)}); err != nil {
			return err
		}
		cfg.Profiles[o.profile] = base
		if err = saveConfig(cfg); err != nil {
			return err
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "Instance URL saved to your profile; API token saved to the OS keyring.")
		return nil
	}}
	login.Flags().BoolVar(&stdin, "token-stdin", false, "Read token from stdin")
	status := &cobra.Command{Use: "status", Short: "Report local credential availability; --check verifies API access", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		base, token, err := o.credentials()
		if err != nil {
			return err
		}
		check, _ := cmd.Flags().GetBool("check")
		if check {
			c, err := o.client()
			if err != nil {
				return err
			}
			if _, err = c.JSON(cmd.Context(), "GET", "documents/?page_size=1", nil); err != nil {
				return err
			}
		}
		return write(cmd, map[string]any{"profile": o.profile, "url": base, "token_available": token != "", "verified": check}, o.output)
	}}
	status.Flags().Bool("check", false, "Verify API access")
	logout := &cobra.Command{Use: "logout", Short: "Remove this profile's saved credentials", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if o.dryRun {
			return invalid("auth logout does not support --dry-run")
		}
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		base := cfg.Profiles[o.profile]
		if base != "" {
			ring, err := openRing()
			if err != nil {
				return err
			}
			if err = ring.Remove(credentialKey(o.profile, base)); err != nil && err != keyring.ErrKeyNotFound {
				return err
			}
		}
		delete(cfg.Profiles, o.profile)
		if err = saveConfig(cfg); err != nil {
			return err
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "Saved credentials removed. Environment variables are unchanged.")
		return nil
	}}
	r.AddCommand(login, status, logout)
	return r
}

// loginInput prompts only with a terminal, leaving stdin available for token automation.
// The password reader is injected so tests never access a real terminal or keyring.
func loginInput(in io.Reader, out io.Writer, base, token string, askURL, tokenStdin bool, password func() ([]byte, error)) (string, string, error) {
	if askURL && !tokenStdin && password != nil {
		if base == "" {
			fmt.Fprint(out, "Paperless URL: ")
		} else {
			fmt.Fprintf(out, "Paperless URL [%s]: ", base)
		}
		line, err := readPromptLine(in)
		if err != nil {
			return "", "", err
		}
		if line != "" {
			base = line
		}
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return "", "", invalid("instance URL is required; run paperless auth login interactively or supply --url")
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	// Validate the URL before requesting a secret or contacting the server.
	if _, err := api.New(base, "validation", time.Second); err != nil {
		return "", "", invalid("%v", err)
	}
	if tokenStdin {
		b, err := io.ReadAll(io.LimitReader(in, 65537))
		if err != nil {
			return "", "", err
		}
		if len(b) > 65536 {
			return "", "", invalid("API token input is too large")
		}
		token = string(b)
	} else if token == "" {
		if password == nil {
			return "", "", invalid("API token is required; run paperless auth login interactively or use --url with --token-stdin")
		}
		fmt.Fprint(out, "API token (hidden): ")
		b, err := password()
		fmt.Fprintln(out)
		if err != nil {
			return "", "", err
		}
		token = string(b)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", "", invalid("API token cannot be empty")
	}
	return base, token, nil
}

// Read exactly one line so buffered reads cannot consume a subsequently pasted token.
func readPromptLine(in io.Reader) (string, error) {
	var line strings.Builder
	var b [1]byte
	for {
		_, err := io.ReadFull(in, b[:])
		if err != nil {
			return "", fmt.Errorf("reading instance URL: %w", err)
		}
		if b[0] == '\n' {
			return strings.TrimSpace(line.String()), nil
		}
		line.WriteByte(b[0])
		if line.Len() > 8192 {
			return "", invalid("instance URL is too long")
		}
	}
}
