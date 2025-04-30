package proxyquerylookup

import (
	"encoding/json"
	"fmt"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
)

func init() {
	caddy.RegisterModule(ProxyQueryLookup{})
}

// ProxyQueryLookup is a Caddy HTTP handler that appends a dynamic query parameter.
type ProxyQueryLookup struct {
	LookupURL string `json:"lookup_url,omitempty"` // The URL to fetch the key
	KeyParam  string `json:"key_param,omitempty"`  // The key parameter to pass dynamically
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
	return nil
}

// ServeHTTP intercepts the request, fetches the key, and appends it as a query parameter.
func (s *ProxyQueryLookup) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// Extract the domain from the Host header
	domain, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		return caddyhttp.Error(http.StatusBadRequest, fmt.Errorf("invalid host: %v", err))
	}

	// Call the lookup endpoint to fetch the key
	key, err := s.fetchStoreID(domain)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

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
// Syntax:
//
//	proxyquerylookup {
//	    lookup_url <url>
//	    key_param  <name>   # Optional, default "key"
//	}
func (s *ProxyQueryLookup) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	// Consume directive name "proxyquerylookup"
	if !d.Next() {
		return d.ArgErr() // Should not happen
	}

	// No args allowed directly after directive name
	if d.NextArg() {
		return d.ArgErr()
	}

	// Expect a block
	if !d.NextBlock(0) {
		return d.Err("expected block following proxyquerylookup directive")
	}

	// Parse options within the block
	for d.NextBlock(0) { // Changed loop condition
		switch d.Val() {
		case "lookup_url":
			if !d.NextArg() { // Check for argument
				return d.ArgErr()
			}
			s.LookupURL = d.Val() // Get argument value
			if d.NextArg() {      // Check for extra arguments
				return d.ArgErr()
			}
		case "key_param":
			if !d.NextArg() { // Check for argument
				return d.ArgErr()
			}
			s.KeyParam = d.Val() // Get argument value
			if d.NextArg() {     // Check for extra arguments
				return d.ArgErr()
			}
		default:
			return d.Errf("unrecognized subdirective '%s'", d.Val())
		}
	}

	// Optional: Add a final check after the block if needed,
	// though Validate() is usually better for required fields.
	// if d.Next() {
	//     return d.Err("unexpected tokens after proxyquerylookup block")
	// }

	return nil
}

// Add Validate method (implements caddy.Validator)
func (s *ProxyQueryLookup) Validate() error {
	if s.LookupURL == "" {
		// This check ensures lookup_url was provided either directly
		// or via an environment variable that was actually set.
		return fmt.Errorf("lookup_url is required and was not provided")
	}
	// Could add URL parsing validation here too
	// if _, err := url.ParseRequestURI(s.LookupURL); err != nil {
	// 	return fmt.Errorf("invalid lookup_url: %v", err)
	// }
	if s.KeyParam == "" {
		// This shouldn't happen if Provision sets a default,
		// but good practice to validate critical params.
		return fmt.Errorf("key_param is required (should have default)")
	}
	return nil
}

// Ensure the interface guard includes Validator
var (
	_ caddy.Provisioner           = (*ProxyQueryLookup)(nil) // Added Provisioner guard
	_ caddy.Validator             = (*ProxyQueryLookup)(nil) // Added Validator guard
	_ caddyhttp.MiddlewareHandler = (*ProxyQueryLookup)(nil)
	_ caddyfile.Unmarshaler       = (*ProxyQueryLookup)(nil)
)
