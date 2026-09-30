package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPaginationAndAuth(t *testing.T) {
	calls := 0
	var s *httptest.Server
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Token secret" {
			t.Error("missing token")
		}
		if r.URL.Path != "/paperless/api/documents/" {
			t.Error(r.URL.Path)
		}
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"results":[{"id":2}],"next":null}`)
		} else {
			fmt.Fprintf(w, `{"results":[{"id":1}],"next":%q}`, s.URL+"/paperless/api/documents/?page=2")
		}
	}))
	defer s.Close()
	c, _ := New(s.URL+"/paperless", "secret", time.Second)
	v, err := c.List(context.Background(), "documents/", true)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || v.(map[string]any)["count"] != 2 {
		t.Fatalf("%v, %d calls", v, calls)
	}
}

func TestURLBoundaries(t *testing.T) {
	c, _ := New("https://example.test/paperless", "secret", time.Second)
	for _, path := range []string{"https://evil.test/api/", "//evil.test/api/", "/outside", "../token/", "documents/%2e%2e/%2e%2e/", "https://user@example.test/paperless/api/", "documents/%5cfoo"} {
		t.Run(path, func(t *testing.T) {
			if _, err := c.URL(path); err == nil {
				t.Fatal("accepted unsafe path")
			}
		})
	}
	for _, path := range []string{"documents/42/", "https://example.test/paperless/api/tags/?page=2"} {
		if _, err := c.URL(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRedirectAndErrors(t *testing.T) {
	for _, status := range []int{302, 400, 401, 403, 404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://example.test")
				w.WriteHeader(status)
				fmt.Fprint(w, "failure secret")
			}))
			defer s.Close()
			c, _ := New(s.URL, "secret", time.Second)
			_, err := c.JSON(context.Background(), "GET", "documents/", nil)
			var ae *Error
			if !errors.As(err, &ae) || ae.Status != status {
				t.Fatalf("%v", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("token leaked")
			}
		})
	}
}

func TestPaginationRejectsCycleAndForeignHost(t *testing.T) {
	for _, next := range []string{"documents/", "https://evil.test/api/documents/"} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"results":[],"next":%q}`, next) }))
		c, _ := New(s.URL, "secret", time.Second)
		_, err := c.List(context.Background(), "documents/", true)
		s.Close()
		if err == nil {
			t.Fatal("expected pagination error")
		}
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := New("https://example.test", "secret", time.Second)
	_, err := c.JSON(ctx, "GET", "documents/", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}
