package networkguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Guard struct {
	allowed    map[string]struct{}
	resolver   *net.Resolver
	publicOnly bool
}

type Resolved struct {
	URL  *url.URL
	IPs  []net.IP
	Port string
}

func New(allowedHosts []string) *Guard {
	return newGuard(allowedHosts, false)
}

// NewPublic creates a guard for internet trust endpoints such as OIDC
// discovery and JWKS. Unlike New, it rejects private and carrier-grade NAT
// destinations even when the hostname is explicitly allowlisted.
func NewPublic(allowedHosts []string) *Guard {
	return newGuard(allowedHosts, true)
}

func newGuard(allowedHosts []string, publicOnly bool) *Guard {
	allowed := make(map[string]struct{}, len(allowedHosts))
	for _, host := range allowedHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			allowed[host] = struct{}{}
		}
	}
	return &Guard{allowed: allowed, resolver: net.DefaultResolver, publicOnly: publicOnly}
}

func (g *Guard) Resolve(ctx context.Context, rawURL string) (Resolved, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return Resolved{}, fmt.Errorf("outbound URL must be absolute")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Resolved{}, fmt.Errorf("outbound URL scheme must be http or https")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return Resolved{}, fmt.Errorf("outbound URL must not contain userinfo or fragment")
	}
	host := strings.ToLower(parsed.Hostname())
	if _, ok := g.allowed[host]; !ok {
		return Resolved{}, fmt.Errorf("outbound host %q is not allowlisted", host)
	}
	ips, err := g.resolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return Resolved{}, fmt.Errorf("resolve outbound host %q: %w", host, err)
	}
	for _, ip := range ips {
		if prohibitedIP(ip, g.publicOnly) {
			return Resolved{}, fmt.Errorf("outbound host %q resolved to prohibited address %s", host, ip)
		}
	}
	sort.Slice(ips, func(i, j int) bool { return ips[i].String() < ips[j].String() })
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return Resolved{URL: parsed, IPs: ips, Port: port}, nil
}

func (g *Guard) Client(ctx context.Context, rawURL string, timeout time.Duration) (*http.Client, *url.URL, error) {
	resolved, err := g.Resolve(ctx, rawURL)
	if err != nil {
		return nil, nil, err
	}
	dialer := &net.Dialer{Timeout: minDuration(timeout, 10*time.Second), KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   minDuration(timeout, 10*time.Second),
		ResponseHeaderTimeout: timeout,
		DialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
			host, port, splitErr := net.SplitHostPort(address)
			if splitErr != nil {
				return nil, splitErr
			}
			if !strings.EqualFold(strings.Trim(host, "[]"), resolved.URL.Hostname()) || port != resolved.Port {
				return nil, fmt.Errorf("unexpected outbound dial target")
			}
			var lastErr error
			for _, ip := range resolved.IPs {
				conn, dialErr := dialer.DialContext(dialCtx, network, net.JoinHostPort(ip.String(), port))
				if dialErr == nil {
					return conn, nil
				}
				lastErr = dialErr
			}
			return nil, lastErr
		},
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return client, resolved.URL, nil
}

var carrierGradeNAT = &net.IPNet{IP: net.ParseIP("100.64.0.0"), Mask: net.CIDRMask(10, 32)}

func prohibitedIP(ip net.IP, publicOnly bool) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() {
		return true
	}
	if ip.Equal(net.ParseIP("169.254.169.254")) || ip.Equal(net.ParseIP("fd00:ec2::254")) {
		return true
	}
	if publicOnly && (ip.IsPrivate() || carrierGradeNAT.Contains(ip)) {
		return true
	}
	return false
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
