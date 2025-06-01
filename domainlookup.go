package domainlookup

import (
	"encoding/json"
	"fmt"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/patrickmn/go-cache"
	"go.uber.org/zap"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func init() {
	caddy.RegisterModule(DomainLookup{})
	httpcaddyfile.RegisterHandlerDirective("domain_lookup", parseCaddyfile)
}

type DomainLookup struct {
	LookupURL string        `json:"lookup_url,omitempty"`
	CacheTTL  time.Duration `json:"cache_ttl,omitempty"`

	logger *zap.Logger
	cache  *cache.Cache
}

func (DomainLookup) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.domain_lookup",
		New: func() caddy.Module { return new(DomainLookup) },
	}
}

func (s *DomainLookup) Provision(ctx caddy.Context) error {
	s.logger = ctx.Logger(s)

	// Set default CacheTTL to 10 minutes if not provided
	if s.CacheTTL == 0 {
		s.CacheTTL = 10 * time.Minute
	}

	// Initialize the cache with the specified TTL
	s.cache = cache.New(s.CacheTTL, 2*s.CacheTTL)

	s.logger.Info("DomainLookup provisioned",
		zap.String("lookup_url", s.LookupURL),
		zap.Duration("cache_ttl", s.CacheTTL),
	)
	return nil
}

func (s *DomainLookup) Validate() error {
	if s.LookupURL == "" {
		return fmt.Errorf("lookup_url is required and was not provided")
	}
	return nil
}

func (s *DomainLookup) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	domain := r.URL.Hostname()
	if domain == "" {
		domain = r.Host
		if colonIndex := strings.Index(domain, ":"); colonIndex != -1 {
			domain = domain[:colonIndex]
		}
	}

	s.logger.Debug("Extracted domain for lookup", zap.String("domain", domain), zap.String("original_host", r.Host))

	dataMap, err := s.fetchDataFromAPI(domain)
	if err != nil {
		s.logger.Error("failed to fetch data from API", zap.String("domain", domain), zap.Error(err))
		return next.ServeHTTP(w, r)
	}

	s.logger.Debug("Successfully fetched data from API", zap.String("domain", domain), zap.Any("data", dataMap))

	s.addHeadersFromMap(r, dataMap)

	return next.ServeHTTP(w, r)
}

func (s *DomainLookup) fetchDataFromAPI(domain string) (map[string]string, error) {
	// Check if the result is already cached
	if cachedData, found := s.cache.Get(domain); found {
		s.logger.Debug("Cache hit for domain", zap.String("domain", domain))
		return cachedData.(map[string]string), nil
	}

	// Cache miss, proceed to fetch data from the API
	lookupURLStr := fmt.Sprintf("%s?domain=%s", s.LookupURL, url.QueryEscape(domain))
	s.logger.Debug("Calling lookup URL", zap.String("url", lookupURLStr))

	resp, err := http.Get(lookupURLStr)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from %s: %w", lookupURLStr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		s.logger.Warn("lookup endpoint returned non-OK status",
			zap.String("url", lookupURLStr),
			zap.Int("status_code", resp.StatusCode),
			zap.ByteString("response_preview", bodyBytes),
		)
		return nil, fmt.Errorf("lookup endpoint %s returned status %d", lookupURLStr, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body from %s: %w", lookupURLStr, err)
	}

	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		s.logger.Error("failed to parse response JSON", zap.String("url", lookupURLStr), zap.ByteString("response_body", body), zap.Error(err))
		return nil, fmt.Errorf("failed to parse response JSON from %s: %w", lookupURLStr, err)
	}

	// Store the result in the cache
	s.cache.Set(domain, result, s.CacheTTL)
	s.logger.Debug("Cache updated for domain", zap.String("domain", domain))

	return result, nil
}

func (s *DomainLookup) addHeadersFromMap(r *http.Request, data map[string]string) {
	if data == nil {
		return
	}

	for key, value := range data {
		r.Header.Set(key, value)
		s.logger.Debug("Added request header", zap.String("key", key), zap.String("value", value))
	}
}

func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var m DomainLookup
	err := m.UnmarshalCaddyfile(h.Dispenser)
	return &m, err
}

func (s *DomainLookup) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		if d.NextArg() {
			return d.ArgErr()
		}
		for d.NextBlock(0) {
			key := d.Val()
			var value string
			if !d.AllArgs(&value) {
				return d.ArgErr()
			}
			switch key {
			case "lookup_url":
				s.LookupURL = value
			case "cache_ttl":
				ttl, err := time.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid cache_ttl value: %v", err)
				}
				s.CacheTTL = ttl
			default:
				return d.Errf("unrecognized subdirective '%s'", d.Val())
			}
		}
	}
	return nil
}

var (
	_ caddy.Module                = (*DomainLookup)(nil)
	_ caddy.Provisioner           = (*DomainLookup)(nil)
	_ caddy.Validator             = (*DomainLookup)(nil)
	_ caddyhttp.MiddlewareHandler = (*DomainLookup)(nil)
	_ caddyfile.Unmarshaler       = (*DomainLookup)(nil)
)
