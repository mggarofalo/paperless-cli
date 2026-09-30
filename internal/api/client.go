// Package api implements the Paperless-ngx REST transport.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Error struct {
	Status int
	Body   string
}

func (e *Error) Error() string { return fmt.Sprintf("Paperless API error %d: %s", e.Status, e.Body) }

type Client struct {
	Base  *url.URL
	Token string
	HTTP  *http.Client
}

func New(base, token string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(base, "/") + "/")
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("PAPERLESS_URL must be an http(s) instance URL without credentials, query, or fragment")
	}
	if token == "" {
		return nil, fmt.Errorf("missing token: set PAPERLESS_API_TOKEN or run paperless auth login")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("timeout must be positive")
	}
	if !strings.HasSuffix(u.Path, "/api/") {
		u.Path += "api/"
	}
	c := &Client{Base: u, Token: token}
	c.HTTP = &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// Redirects can change mutation semantics and leak credentials. Require the canonical URL.
		return http.ErrUseLastResponse
	}}
	return c, nil
}

// URL confines pagination and raw requests to this instance's API namespace.
func (c *Client) URL(path string) (*url.URL, error) {
	r, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	u := c.Base.ResolveReference(r)
	if u.Scheme != c.Base.Scheme || u.Host != c.Base.Host || u.User != nil || u.Fragment != "" || !strings.HasPrefix(u.Path, c.Base.Path) || strings.Contains(u.Path, "\\") {
		return nil, fmt.Errorf("request must stay within the configured Paperless API")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == ".." || part == "." {
			return nil, fmt.Errorf("invalid API path")
		}
	}
	return u, nil
}

// Reverse proxies may leave HTTP in server-generated pagination links. Upgrade
// only links for the same instance; never send credentials over a downgraded URL.
func (c *Client) paginationURL(path string) (*url.URL, error) {
	r, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	if c.Base.Scheme == "https" && r.Scheme == "http" && strings.EqualFold(r.Hostname(), c.Base.Hostname()) {
		samePort := r.Port() == c.Base.Port()
		standardPorts := (r.Port() == "" || r.Port() == "80") && (c.Base.Port() == "" || c.Base.Port() == "443")
		if samePort || standardPorts {
			r.Scheme = c.Base.Scheme
			r.Host = c.Base.Host
		}
	}
	return c.URL(r.String())
}

func (c *Client) Request(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	u, err := c.URL(path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+c.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "paperless-cli")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, &Error{resp.StatusCode, strings.ReplaceAll(strings.TrimSpace(string(b)), c.Token, "[REDACTED]")}
	}
	return resp, nil
}

func (c *Client) JSON(ctx context.Context, method, path string, body any) (any, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	resp, err := c.Request(ctx, method, path, reader, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	var v any
	d := json.NewDecoder(resp.Body)
	d.UseNumber()
	err = d.Decode(&v)
	if err == io.EOF {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("decode API JSON: %w", err)
	}
	return v, nil
}

func (c *Client) List(ctx context.Context, path string, all bool) (any, error) {
	if !all {
		return c.JSON(ctx, "GET", path, nil)
	}
	results := []any{}
	seen := map[string]bool{}
	for path != "" {
		u, err := c.URL(path)
		if err != nil {
			return nil, err
		}
		if seen[u.String()] {
			return nil, fmt.Errorf("pagination cycle detected")
		}
		seen[u.String()] = true
		v, err := c.JSON(ctx, "GET", u.String(), nil)
		if err != nil {
			return nil, err
		}
		if items, ok := v.([]any); ok {
			results = append(results, items...)
			break
		}
		page, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected paginated API object")
		}
		items, ok := page["results"].([]any)
		if !ok {
			return nil, fmt.Errorf("API page has no results array")
		}
		results = append(results, items...)
		next := page["next"]
		if next == nil {
			break
		}
		path, ok = next.(string)
		if !ok {
			return nil, fmt.Errorf("invalid pagination link")
		}
		if strings.HasPrefix(path, "?") {
			ref, err := url.Parse(path)
			if err != nil {
				return nil, err
			}
			path = u.ResolveReference(ref).String()
		}
		if path != "" {
			nextURL, err := c.paginationURL(path)
			if err != nil {
				return nil, err
			}
			path = nextURL.String()
		}
	}
	return map[string]any{"count": len(results), "results": results}, nil
}
