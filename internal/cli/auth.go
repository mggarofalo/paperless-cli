package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	login := &cobra.Command{Use: "login", Short: "Validate and save token to the OS keyring", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if o.dryRun {
			return invalid("auth login does not support --dry-run")
		}
		base, token, err := o.credentials()
		if err != nil {
			return err
		}
		if stdin {
			b, e := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 65536))
			if e != nil {
				return e
			}
			token = strings.TrimSpace(string(b))
		} else if token == "" {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return invalid("set PAPERLESS_API_TOKEN or use --token-stdin")
			}
			fmt.Fprint(cmd.ErrOrStderr(), "API token: ")
			b, e := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(cmd.ErrOrStderr())
			if e != nil {
				return e
			}
			token = strings.TrimSpace(string(b))
		}
		c, err := api.New(base, token, o.timeout)
		if err != nil {
			return err
		}
		if _, err = c.JSON(cmd.Context(), "GET", "documents/?page_size=1", nil); err != nil {
			return err
		}
		cfg, err := loadConfig()
		if err != nil {
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
		fmt.Fprintln(cmd.ErrOrStderr(), "Credentials saved to OS keyring.")
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
