package proxyquerylookup

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func init() {
	caddy.RegisterModule(ProxyQueryLookup{})
}

// ProxyQueryLookup is a Caddy HTTP handler that appends a dynamic query parameter.
type ProxyQueryLookup struct {
	LookupURL string        `json:"lookup_url,omitempty"` // The URL to fetch the key
	KeyParam  string        `json:"key_param,omitempty"`  // The key parameter to pass dynamically
	CacheTTL  time.Duration `json:"cache_ttl,omitempty"`  // Cache expiration time in seconds
	cache     sync.Map      // In-memory cache for key values
}

// CaddyModule returns the Caddy module information.
func (ProxyQueryLookup) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.proxyquerylookup",
		New: func() caddy.Module { return new(ProxyQueryLookup) },
	}
}

// Provision sets up the module.
func (s *ProxyQueryLookup) Provision(ctx caddy.Context) error {
	if s.LookupURL == "" {
		return fmt.Errorf("lookup_url is required")
	}
	if s.KeyParam == "" {
		s.KeyParam = "key" // Default key parameter
	}
	if s.CacheTTL == 0 {
		s.CacheTTL = 60 * time.Second // Default cache TTL
	}
	return nil
}

// ServeHTTP intercepts the request, fetches the key, and appends it as a query parameter.
func (s *ProxyQueryLookup) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// Extract the domain from the Host header
	domain := r.Host
	if domain == "" {
		return caddyhttp.Error(http.StatusBadRequest, fmt.Errorf("missing Host header"))
	}

	// Check the cache for the key
	if cached, ok := s.cache.Load(domain); ok {
		if entry, valid := cached.(cacheEntry); valid && time.Since(entry.timestamp) < s.CacheTTL {
			// Use cached value
			s.appendQueryParam(r, entry.value)
			return next.ServeHTTP(w, r)
		}
	}

	// Call the lookup endpoint to fetch the key
	key, err := s.fetchStoreID(domain)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

	// Cache the key
	s.cache.Store(domain, cacheEntry{value: key, timestamp: time.Now()})

	// Append the key as a query parameter
	s.appendQueryParam(r, key)

	// Pass the modified request to the next handler
	return next.ServeHTTP(w, r)
}

// fetchStoreID calls the lookup endpoint and retrieves the key.
func (s *ProxyQueryLookup) fetchStoreID(domain string) (string, error) {
	// Build the lookup URL
	lookupURL := fmt.Sprintf("%s?domain=%s", s.LookupURL, url.QueryEscape(domain))

	// Make the HTTP request
	resp, err := http.Get(lookupURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch key: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("lookup endpoint returned status %d", resp.StatusCode)
	}

	// Parse the response
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response JSON: %v", err)
	}

	// Extract the key from the response
	key, ok := result[s.KeyParam].(string)
	if !ok {
		return "", fmt.Errorf("%s not found in response", s.KeyParam)
	}

	return key, nil
}

// appendQueryParam appends the key as a query parameter to the request URL.
func (s *ProxyQueryLookup) appendQueryParam(r *http.Request, value string) {
	query := r.URL.Query()
	query.Set(s.KeyParam, value)
	r.URL.RawQuery = query.Encode()
}

// UnmarshalCaddyfile sets up the module from Caddyfile tokens.
func (s *ProxyQueryLookup) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		args := d.RemainingArgs()
		if len(args) > 0 {
			s.LookupURL = args[0]
		}
		for d.NextBlock(0) {
			switch d.Val() {
			case "key_param":
				if !d.Args(&s.KeyParam) {
					return d.ArgErr()
				}
			case "cache_ttl":
				var ttlStr string
				if !d.Args(&ttlStr) {
					return d.ArgErr()
				}
				ttl, err := strconv.Atoi(ttlStr)
				if err != nil {
					return fmt.Errorf("invalid cache_ttl value: %v", err)
				}
				s.CacheTTL = time.Duration(ttl) * time.Second
			}
		}
	}
	return nil
}

// cacheEntry represents a cached key value with a timestamp.
type cacheEntry struct {
	value     string
	timestamp time.Time
}

// Interface guards
var (
	_ caddyhttp.MiddlewareHandler = (*ProxyQueryLookup)(nil)
	_ caddyfile.Unmarshaler       = (*ProxyQueryLookup)(nil)
)
