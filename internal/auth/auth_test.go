package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.10:54321"
	req.Header.Set("X-Real-IP", "10.0.0.9")
	if got := ClientIP(req, false); got != "203.0.113.10" {
		t.Fatalf("got %q, want TCP peer", got)
	}
}

func TestClientIPTrustRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.10:54321"
	req.Header.Set("X-Real-IP", "198.51.100.7")
	req.Header.Set("X-Forwarded-For", "192.0.2.1, 10.0.0.1")
	if got := ClientIP(req, true); got != "198.51.100.7" {
		t.Fatalf("got %q, want X-Real-IP", got)
	}
}

func TestClientIPTrustForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.10:54321"
	req.Header.Set("X-Forwarded-For", "not-an-ip, 192.0.2.44, 10.0.0.1")
	if got := ClientIP(req, true); got != "192.0.2.44" {
		t.Fatalf("got %q, want first valid XFF hop", got)
	}
}

func TestClientIPMappedIPv6(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[::ffff:192.168.1.10]:80"
	if got := ClientIP(req, false); got != "192.168.1.10" {
		t.Fatalf("got %q, want mapped IPv4", got)
	}
}

func TestIPAllowed(t *testing.T) {
	if IPAllowed("1.2.3.4", nil) {
		t.Fatal("empty list must not match")
	}
	if !IPAllowed("1.2.3.4", []string{"1.2.3.4"}) {
		t.Fatal("exact IP should match")
	}
	if !IPAllowed("10.1.2.3", []string{"10.0.0.0/8"}) {
		t.Fatal("CIDR should contain")
	}
	if IPAllowed("11.1.2.3", []string{"10.0.0.0/8"}) {
		t.Fatal("outside CIDR")
	}
	if IPAllowed("1.2.3.4", []string{"2001:db8::/32"}) {
		t.Fatal("IPv4 must not match IPv6 CIDR")
	}
	if !IPAllowed("::ffff:10.9.8.7", []string{"10.0.0.0/8"}) {
		t.Fatal("mapped IPv6 should match IPv4 CIDR")
	}
	if IPAllowed("1.2.3.4", []string{"not-an-ip", "9.9.9.9"}) {
		t.Fatal("invalid entries skipped")
	}
}

func TestNormalizeAllowedIPs(t *testing.T) {
	got, err := NormalizeAllowedIPs([]string{" 127.0.0.1 ", "", "10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "127.0.0.1" || got[1] != "10.0.0.0/8" {
		t.Fatalf("got %#v", got)
	}
	if _, err := NormalizeAllowedIPs([]string{"nope"}); err == nil {
		t.Fatal("expected error")
	}
}
