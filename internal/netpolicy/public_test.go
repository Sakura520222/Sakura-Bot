// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package netpolicy

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPublicURL(t *testing.T) {
	got, err := PublicURL("https://example.com/articles/guide#part")
	if err != nil || got != "https://example.com/articles/guide" {
		t.Fatalf("public URL = %q, %v", got, err)
	}
	for _, raw := range []string{
		"http://localhost/",
		"http://192.0.2.1/",
		"https://example.com:8443/",
		"https://user@example.com/",
		"https://example.com/has space",
		"https://[2002:c000:201::]/",
	} {
		if _, err := PublicURL(raw); err == nil {
			t.Errorf("accepted disallowed URL %q", raw)
		}
	}
}

type fixtureResolver []netip.Addr

func (r fixtureResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r, nil
}

func TestPublicHTTPClientRejectsNonPublicDNSAnswers(t *testing.T) {
	client := NewPublicHTTPClient(time.Second, fixtureResolver{
		netip.MustParseAddr("8.8.8.8"),
		netip.MustParseAddr("192.0.2.1"),
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	conn, err := client.Transport.(*http.Transport).DialContext(ctx, "tcp", "example.com:443")
	if conn != nil {
		defer conn.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "non-public IP address blocked") {
		t.Fatalf("expected DNS address rejection, got %v", err)
	}
}

func TestSameOrigin(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"https://EXAMPLE.com", "https://example.com:443/v1", true},
		{"http://example.com", "https://example.com", false},
		{"https://σ.example", "https://ς.example", false},
		{"http://[fe80::1%25eth0]", "http://[fe80::1%25ETH0]", false},
	} {
		a, err := url.Parse(tc.left)
		if err != nil {
			t.Fatal(err)
		}
		b, err := url.Parse(tc.right)
		if err != nil {
			t.Fatal(err)
		}
		if got := SameOrigin(a, b); got != tc.same {
			t.Errorf("SameOrigin(%q, %q) = %t, want %t", tc.left, tc.right, got, tc.same)
		}
	}
}
