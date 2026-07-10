package oidcauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/auth"
	"github.com/DizzyZ7/StormRelay/internal/networkguard"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	maxTokenBytes     = 128 << 10
	maxDiscoveryBytes = 1 << 20
	outboundTimeout   = 10 * time.Second
)

var ErrInvalidCredential = errors.New("invalid OIDC credential")

type Discovery struct {
	Issuer               string
	JWKSURI              string
	SupportedSigningAlgs []string
}

type Service struct {
	store *storage.Store
	mu    sync.Mutex
	cache map[string]*oidc.IDTokenVerifier
}

func NewService(store *storage.Store) *Service {
	return &Service{store: store, cache: map[string]*oidc.IDTokenVerifier{}}
}

func Discover(ctx context.Context, issuer string) (Discovery, error) {
	issuer = strings.TrimSpace(issuer)
	parsedIssuer, err := validateHTTPSURL(issuer)
	if err != nil {
		return Discovery{}, fmt.Errorf("issuer: %w", err)
	}
	discoveryURL := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
	guard := networkguard.New([]string{parsedIssuer.Hostname()})
	client, _, err := guard.Client(ctx, discoveryURL, outboundTimeout)
	if err != nil {
		return Discovery{}, fmt.Errorf("guard discovery endpoint: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return Discovery{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Discovery{}, fmt.Errorf("fetch discovery document: %w", err)
	}
	defer resp.Body.Close()
	body, err := readBounded(resp.Body, maxDiscoveryBytes)
	if err != nil {
		return Discovery{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Discovery{}, fmt.Errorf("discovery endpoint returned HTTP %d", resp.StatusCode)
	}
	var document struct {
		Issuer               string   `json:"issuer"`
		JWKSURI              string   `json:"jwks_uri"`
		SupportedSigningAlgs []string `json:"id_token_signing_alg_values_supported"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return Discovery{}, fmt.Errorf("decode discovery document: %w", err)
	}
	if document.Issuer != issuer {
		return Discovery{}, fmt.Errorf("discovery issuer mismatch")
	}
	parsedJWKS, err := validateHTTPSURL(document.JWKSURI)
	if err != nil {
		return Discovery{}, fmt.Errorf("jwks_uri: %w", err)
	}
	if _, err := networkguard.New([]string{parsedJWKS.Hostname()}).Resolve(ctx, document.JWKSURI); err != nil {
		return Discovery{}, fmt.Errorf("guard jwks_uri: %w", err)
	}
	algs := safeSigningAlgs(document.SupportedSigningAlgs)
	if len(algs) == 0 {
		algs = []string{oidc.RS256}
	}
	return Discovery{Issuer: issuer, JWKSURI: document.JWKSURI, SupportedSigningAlgs: algs}, nil
}

func (s *Service) Authenticate(ctx context.Context, rawToken string) (auth.Principal, error) {
	hint, err := parseUnverifiedRoutingClaims(rawToken)
	if err != nil {
		return auth.Principal{}, ErrInvalidCredential
	}
	provider, err := s.store.FindOIDCProvider(ctx, hint.Issuer, hint.Audiences)
	if err != nil {
		if storage.IsNoRows(err) {
			return auth.Principal{}, ErrInvalidCredential
		}
		return auth.Principal{}, err
	}
	verifier, err := s.verifier(ctx, provider)
	if err != nil {
		return auth.Principal{}, err
	}
	verifyCtx, cancel := context.WithTimeout(ctx, outboundTimeout)
	defer cancel()
	verified, err := verifier.Verify(verifyCtx, rawToken)
	if err != nil {
		return auth.Principal{}, ErrInvalidCredential
	}
	if verified.Subject == "" || verified.Issuer != provider.Issuer {
		return auth.Principal{}, ErrInvalidCredential
	}
	principal, err := s.store.AuthenticateOIDCIdentity(ctx, provider.ID, verified.Subject)
	if err != nil {
		if storage.IsNoRows(err) {
			return auth.Principal{}, ErrInvalidCredential
		}
		return auth.Principal{}, err
	}
	return principal, nil
}

func (s *Service) verifier(ctx context.Context, provider storage.OIDCProvider) (*oidc.IDTokenVerifier, error) {
	cacheKey := fmt.Sprintf("%s:%d", provider.ID, provider.Version)
	s.mu.Lock()
	defer s.mu.Unlock()
	if verifier, ok := s.cache[cacheKey]; ok {
		return verifier, nil
	}
	parsedJWKS, err := validateHTTPSURL(provider.JWKSURI)
	if err != nil {
		return nil, err
	}
	client, _, err := networkguard.New([]string{parsedJWKS.Hostname()}).Client(ctx, provider.JWKSURI, outboundTimeout)
	if err != nil {
		return nil, fmt.Errorf("guard OIDC key endpoint: %w", err)
	}
	keyContext := oidc.ClientContext(context.Background(), client)
	keySet := oidc.NewRemoteKeySet(keyContext, provider.JWKSURI)
	verifier := oidc.NewVerifier(provider.Issuer, keySet, &oidc.Config{
		ClientID:             provider.Audience,
		SupportedSigningAlgs: provider.SupportedSigningAlgs,
	})
	for key := range s.cache {
		if strings.HasPrefix(key, provider.ID+":") {
			delete(s.cache, key)
		}
	}
	s.cache[cacheKey] = verifier
	return verifier, nil
}

type routingClaims struct {
	Issuer    string
	Audiences []string
}

func parseUnverifiedRoutingClaims(rawToken string) (routingClaims, error) {
	if len(rawToken) == 0 || len(rawToken) > maxTokenBytes {
		return routingClaims{}, ErrInvalidCredential
	}
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 || parts[1] == "" {
		return routingClaims{}, ErrInvalidCredential
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) > 64<<10 {
		return routingClaims{}, ErrInvalidCredential
	}
	var claims struct {
		Issuer   string          `json:"iss"`
		Audience json.RawMessage `json:"aud"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return routingClaims{}, ErrInvalidCredential
	}
	claims.Issuer = strings.TrimSpace(claims.Issuer)
	if _, err := validateHTTPSURL(claims.Issuer); err != nil {
		return routingClaims{}, ErrInvalidCredential
	}
	audiences, err := parseAudiences(claims.Audience)
	if err != nil || len(audiences) == 0 {
		return routingClaims{}, ErrInvalidCredential
	}
	return routingClaims{Issuer: claims.Issuer, Audiences: audiences}, nil
}

func parseAudiences(raw json.RawMessage) ([]string, error) {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		single = strings.TrimSpace(single)
		if single == "" {
			return nil, ErrInvalidCredential
		}
		return []string{single}, nil
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil {
		return nil, ErrInvalidCredential
	}
	out := make([]string, 0, len(multiple))
	seen := map[string]struct{}{}
	for _, value := range multiple {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func safeSigningAlgs(values []string) []string {
	allowed := map[string]struct{}{
		oidc.RS256: {}, oidc.RS384: {}, oidc.RS512: {},
		oidc.PS256: {}, oidc.PS384: {}, oidc.PS512: {},
		oidc.ES256: {}, oidc.ES384: {}, oidc.ES512: {},
	}
	out := []string{}
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if _, ok := allowed[value]; !ok {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func validateHTTPSURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("must be an absolute HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("must not contain userinfo, query, or fragment")
	}
	return parsed, nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: limit + 1}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return body, nil
}
