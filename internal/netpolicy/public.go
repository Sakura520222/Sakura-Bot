// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package netpolicy controls outbound HTTP destinations.
package netpolicy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

var (
	httpURLShape  = regexp.MustCompile(`^(?i:https?)://[^\x00-\x20\x7f\\]+$`)
	reNumericHost = regexp.MustCompile(`^(?i:(?:0x[0-9a-f]+|[0-9]+)(?:\.(?:0x[0-9a-f]+|[0-9]+))*\.?)$`)
	reASCIIAlpha  = regexp.MustCompile(`[a-zA-Z]`)

	// 非公网保留网段黑名单（补充 netip 标准库方法之外的 CGNAT / IETF / TEST-NET / 文档 / 基准测试网段）
	globalIPv6      = netip.MustParsePrefix("2000::/3")
	blockedPrefixes = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),       // RFC 1122 "This network"
		netip.MustParsePrefix("100.64.0.0/10"),   // RFC 6598 CGNAT (Shared Address Space)
		netip.MustParsePrefix("127.0.0.0/8"),     // Loopback
		netip.MustParsePrefix("169.254.0.0/16"),  // Link-local
		netip.MustParsePrefix("192.0.0.0/24"),    // IETF Protocol Assignments
		netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
		netip.MustParsePrefix("198.18.0.0/15"),   // Benchmarking
		netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
		netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
		netip.MustParsePrefix("224.0.0.0/4"),     // Multicast
		netip.MustParsePrefix("240.0.0.0/4"),     // Reserved Class E / Broadcast
		netip.MustParsePrefix("::1/128"),         // IPv6 Loopback
		netip.MustParsePrefix("fc00::/7"),        // IPv6 Unique Local (ULA)
		netip.MustParsePrefix("fe80::/10"),       // IPv6 Link-Local
		netip.MustParsePrefix("ff00::/8"),        // IPv6 Multicast
		netip.MustParsePrefix("2001:db8::/32"),   // IPv6 Documentation
		netip.MustParsePrefix("2001::/23"),       // IETF Protocol Assignments / Teredo
		netip.MustParsePrefix("2002::/16"),       // 6to4 translation
		netip.MustParsePrefix("3fff::/20"),       // IPv6 Documentation
		netip.MustParsePrefix("100::/64"),        // Discard-Only Prefix
		netip.MustParsePrefix("64:ff9b:1::/48"),  // Local-Use IPv4/IPv6 Translation
	}
)

// ParseHTTPURL validates the wire syntax before parsing the authority.
func ParseHTTPURL(raw string) (*url.URL, error) {
	if len(raw) > 2048 || !httpURLShape.MatchString(raw) {
		return nil, errors.New("invalid HTTP URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("invalid HTTP URL authority")
	}
	return u, nil
}

// IsPublicIP 严格判定 IP 地址是否为全球可达公网单播地址（拦截回环、RFC1918 私网、CGNAT、链路本地、组播与保留地址）。
func IsPublicIP(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if addr.Is6() && (!globalIPv6.Contains(addr) || addr.Zone() != "") {
		return false
	}
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() || !addr.IsGlobalUnicast() {
		return false
	}
	for _, pfx := range blockedPrefixes {
		if pfx.Contains(addr) {
			return false
		}
	}
	return true
}

// PublicURL 校验并归一化公开 HTTP/HTTPS URL（仅允许 80/443 默认端口、禁止凭据与私网/本地主机名，对齐 public_url）。
func PublicURL(raw string) (string, error) {
	parsed, err := ParseHTTPURL(raw)
	if err != nil {
		return "", errors.New("invalid public URL")
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if port != "" {
		if (parsed.Scheme == "https" && port != "443") || (parsed.Scheme == "http" && port != "80") {
			return "", errors.New("invalid public URL port")
		}
	}
	cleanHost := strings.TrimRight(strings.ToLower(host), ".")
	if cleanHost == "localhost" || cleanHost == "localhost.localdomain" ||
		strings.HasSuffix(cleanHost, ".local") ||
		strings.HasSuffix(cleanHost, ".internal") ||
		strings.HasSuffix(cleanHost, ".localhost") {
		return "", errors.New("invalid public hostname")
	}

	if ip, ipErr := netip.ParseAddr(host); ipErr == nil {
		if !IsPublicIP(ip) {
			return "", errors.New("invalid public address")
		}
	} else {
		if !strings.Contains(cleanHost, ".") || reNumericHost.MatchString(cleanHost) {
			return "", errors.New("invalid public hostname")
		}
		lastDot := strings.LastIndexByte(cleanHost, '.')
		tld := cleanHost[lastDot+1:]
		if !reASCIIAlpha.MatchString(tld) {
			return "", errors.New("invalid public hostname TLD")
		}
	}

	parsed.Fragment = ""
	return parsed.String(), nil
}

// SameOrigin compares HTTP URL scheme, host and effective port.
func SameOrigin(a, b *url.URL) bool {
	if a.Scheme != b.Scheme || effectivePort(a) != effectivePort(b) {
		return false
	}
	left, right := a.Hostname(), b.Hostname()
	if addr, err := netip.ParseAddr(left); err == nil {
		other, err := netip.ParseAddr(right)
		return err == nil && addr == other
	}
	leftASCII, leftErr := idna.Lookup.ToASCII(left)
	rightASCII, rightErr := idna.Lookup.ToASCII(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(leftASCII, rightASCII)
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// IPResolver 抽象域名解析接口（测试中可注入自定义解析器，生产默认 net.DefaultResolver）。
type IPResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// NewPublicHTTPClient 构造在建立连接前强制校验全部 DNS 解析结果均为全球公网单播 IP 且禁用自动重定向的 *http.Client。
func NewPublicHTTPClient(timeout time.Duration, resolver IPResolver) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := &net.Dialer{Timeout: min(timeout, 10*time.Second)}
	transport := &http.Transport{
		Proxy: nil, // 禁止读取环境代理，防止通过代理绕过内网隔离
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if port != "80" && port != "443" {
				return nil, errors.New("disallowed outbound port")
			}
			var addrs []netip.Addr
			if ip, ipErr := netip.ParseAddr(host); ipErr == nil {
				addrs = []netip.Addr{ip}
			} else {
				addrs, err = resolver.LookupNetIP(ctx, "ip", host)
				if err != nil || len(addrs) == 0 {
					return nil, errors.New("DNS resolution failed")
				}
			}
			for _, addr := range addrs {
				if !IsPublicIP(addr) {
					return nil, errors.New("non-public IP address blocked")
				}
			}
			// 直接拨号已核实的第一个公网 IP，彻底杜绝二次解析 DNS Rebinding TOCTOU
			var lastErr error
			for _, addr := range addrs {
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(addr.Unmap().String(), port))
				if dialErr == nil {
					return conn, nil
				}
				lastErr = dialErr
			}
			return nil, lastErr
		},
		TLSHandshakeTimeout:   min(timeout, 10*time.Second),
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
