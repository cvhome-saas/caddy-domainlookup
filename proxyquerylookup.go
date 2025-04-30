package proxyquerylookup

import (
	"encoding/json"
	"fmt"
	"io" // Use io instead of ioutil
	"net/http"
	"net/url"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	// --- Import needed for Caddyfile directive registration ---
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap" // Import zap for Caddy logging
)

func init() {
	caddy.RegisterModule(ProxyQueryLookup{})
	// --- Add this line to register the Caddyfile directive ---
	httpcaddyfile.RegisterHandlerDirective("proxyquerylookup", parseCaddyfile)
}

// ProxyQueryLookup is a Caddy HTTP handler that appends a dynamic query parameter.
type ProxyQueryLookup struct {
	LookupURL string `json:"lookup_url,omitempty"` // The URL to fetch the key
	KeyParam  string `json:"key_param,omitempty"`  // The key parameter to pass dynamically

	logger *zap.Logger // Add logger field
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
	s.logger = ctx.Logger(s) // Get the logger from the context

	// Defaults are set here, but validation happens in Validate()
	if s.KeyParam == "" {
		s.KeyParam = "key" // Default key parameter
		s.logger.Debug("key_param not set, using default", zap.String("default_key_param", s.KeyParam))
	}
	s.logger.Info("ProxyQueryLookup provisioned",
		zap.String("lookup_url", s.LookupURL),
		zap.String("key_param", s.KeyParam),
	)
	return nil
}

// Validate ensures the configuration is valid after provisioning.
func (s *ProxyQueryLookup) Validate() error {
	if s.LookupURL == "" {
		return fmt.Errorf("lookup_url is required and was not provided")
	}
	// Optional: Validate URL format
	if _, err := url.ParseRequestURI(s.LookupURL); err != nil {
		return fmt.Errorf("invalid lookup_url format: %v", err)
	}
	if s.KeyParam == "" {
		// This shouldn't happen if Provision sets a default, but good practice.
		return fmt.Errorf("key_param is required (should have default)")
	}
	return nil
}

// ServeHTTP intercepts the request, fetches the key, and appends it as a query parameter.
func (s *ProxyQueryLookup) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// Extract the domain from the Host header
	// Consider using r.URL.Hostname() if you don't need the port,
	// or handle the error if the port might be missing.
	domain := r.URL.Hostname() // Simpler way to get hostname without port

	s.logger.Debug("Extracted domain for lookup", zap.String("domain", domain), zap.String("original_host", r.Host))

	// Call the lookup endpoint to fetch the key
	key, err := s.fetchStoreID(domain)
	if err != nil {
		s.logger.Error("failed to fetch store ID", zap.String("domain", domain), zap.Error(err))
		// Decide if you want to block the request or pass it through without the param
		// return caddyhttp.Error(http.StatusInternalServerError, err) // Block request
		return next.ServeHTTP(w, r) // Pass through on error
	}

	s.logger.Debug("Successfully fetched key", zap.String("domain", domain), zap.String("key_param", s.KeyParam), zap.String("value", key))

	// Append the key as a query parameter
	s.appendQueryParam(r, key)

	// Pass the modified request to the next handler
	return next.ServeHTTP(w, r)
}

// fetchStoreID calls the lookup endpoint and retrieves the key.
func (s *ProxyQueryLookup) fetchStoreID(domain string) (string, error) {
	// Build the lookup URL
	lookupURL := fmt.Sprintf("%s?domain=%s", s.LookupURL, url.QueryEscape(domain))
	s.logger.Debug("Calling lookup URL", zap.String("url", lookupURL))

	// Make the HTTP request (Consider using a client with timeout from Provision)
	resp, err := http.Get(lookupURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch key from %s: %v", lookupURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body) // Read body for context
		s.logger.Warn("lookup endpoint returned non-OK status",
			zap.String("url", lookupURL),
			zap.Int("status_code", resp.StatusCode),
			zap.ByteString("response_body", bodyBytes), // Log response body if helpful
		)
		return "", fmt.Errorf("lookup endpoint %s returned status %d", lookupURL, resp.StatusCode)
	}

	// Parse the response
	body, err := io.ReadAll(resp.Body) // Use io.ReadAll
	if err != nil {
		return "", fmt.Errorf("failed to read response body from %s: %v", lookupURL, err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		// Log the body if JSON parsing fails
		s.logger.Error("failed to parse response JSON", zap.String("url", lookupURL), zap.ByteString("response_body", body), zap.Error(err))
		return "", fmt.Errorf("failed to parse response JSON from %s: %v", lookupURL, err)
	}

	// Extract the key from the response
	keyValue, ok := result[s.KeyParam]
	if !ok {
		s.logger.Warn("key_param not found in lookup response", zap.String("url", lookupURL), zap.String("key_param", s.KeyParam), zap.Any("response_json", result))
		return "", fmt.Errorf("key '%s' not found in response from %s", s.KeyParam, lookupURL)
	}

	keyStr, ok := keyValue.(string)
	if !ok {
		s.logger.Warn("key_param value in lookup response is not a string", zap.String("url", lookupURL), zap.String("key_param", s.KeyParam), zap.Any("value", keyValue))
		return "", fmt.Errorf("key '%s' value in response from %s is not a string (type: %T)", s.KeyParam, lookupURL, keyValue)
	}

	if keyStr == "" {
		s.logger.Warn("key_param value in lookup response is empty", zap.String("url", lookupURL), zap.String("key_param", s.KeyParam))
		// Decide if empty key is an error or allowed
		// return "", fmt.Errorf("key '%s' value in response from %s is empty", s.KeyParam, lookupURL)
	}

	return keyStr, nil
}

// appendQueryParam appends the key as a query parameter to the request URL.
func (s *ProxyQueryLookup) appendQueryParam(r *http.Request, value string) {
	query := r.URL.Query()
	query.Set(s.KeyParam, value)
	r.URL.RawQuery = query.Encode()
	s.logger.Debug("Appended query parameter", zap.String("key", s.KeyParam), zap.String("value", value), zap.String("new_url", r.URL.String()))
}

// --- Add this helper function ---
// parseCaddyfile links the Caddyfile directive to the UnmarshalCaddyfile method.
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var m ProxyQueryLookup
	err := m.UnmarshalCaddyfile(h.Dispenser)
	// Return pointer (&m) because ServeHTTP has a pointer receiver
	return &m, err
}

// UnmarshalCaddyfile sets up the module from Caddyfile tokens.
// Syntax:
//
//	proxyquerylookup {
//	    lookup_url <url>
//	    key_param  <name>   # Optional, default "key"
//	}
func (s *ProxyQueryLookup) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	// No need to consume directive name here, parseCaddyfile handles it

	for d.Next() { // Process the line with the directive name

		// No args allowed directly after directive name on the same line
		if d.NextArg() {
			return d.ArgErr()
		}

		// Expect a block { }
		for d.NextBlock(0) { // Enter the block
			switch d.Val() {
			case "lookup_url":
				if !d.AllArgs(&s.LookupURL) { // Use AllArgs for simplicity if only one arg expected
					return d.ArgErr()
				}
			case "key_param":
				if !d.AllArgs(&s.KeyParam) { // Use AllArgs
					return d.ArgErr()
				}
			default:
				return d.Errf("unrecognized subdirective '%s'", d.Val())
			}
		} // Exit block
	} // End processing directive line/block

	// No need for checks like 'if s.LookupURL == ""' here,
	// the Validate() method handles required field checks after parsing.

	return nil
}

// Interface guards ensure the struct implements the necessary interfaces.
// Make sure they are pointers where methods have pointer receivers.
var (
	_ caddy.Module                = (*ProxyQueryLookup)(nil)
	_ caddy.Provisioner           = (*ProxyQueryLookup)(nil)
	_ caddy.Validator             = (*ProxyQueryLookup)(nil)
	_ caddyhttp.MiddlewareHandler = (*ProxyQueryLookup)(nil)
	_ caddyfile.Unmarshaler       = (*ProxyQueryLookup)(nil)
)
