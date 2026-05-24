// Package config resolves runtime configuration from env vars and CLI flags.
//
// Precedence (highest wins): explicit flag → env var → default. Mirrors the
// pattern used by gh / kubectl / aws-cli so it's unsurprising to operators
// piping the CLI into existing CI scripts.
package config

import (
	"errors"
	"os"
	"strings"
)

const (
	// EnvAPIKey holds the account-scoped Observo API key. See
	// https://app.observoai.co/settings/api-keys to create one.
	EnvAPIKey = "OBSERVO_API_KEY"
	// EnvBaseURL overrides the API endpoint. Useful for self-hosted or staging.
	EnvBaseURL = "OBSERVO_BASE_URL"

	// DefaultBaseURL is the hosted Observo API. CLI defaults here when neither
	// flag nor env var sets a URL.
	DefaultBaseURL = "https://api.observoai.co"
)

// Config carries resolved runtime settings.
type Config struct {
	APIKey  string
	BaseURL string
	JSON    bool // true → machine-readable JSON output; false → human text
	Verbose bool // true → debug logging of HTTP requests/responses
}

// ErrMissingAPIKey signals that no API key was provided via flag or env.
// Subcommands that need auth should surface this with a clear hint about
// where to get a key.
var ErrMissingAPIKey = errors.New("OBSERVO_API_KEY not set — pass --api-key or export OBSERVO_API_KEY (create one at https://app.observoai.co/settings/api-keys)")

// Resolve composes a Config from the four flag values + env. Flag values
// of "" are treated as unset, falling back to env / default. Empty BaseURL
// resolves to DefaultBaseURL — but APIKey staying empty is NOT an error
// here; that's deferred to Validate() so commands like `version` / `help`
// don't need an API key.
func Resolve(apiKeyFlag, baseURLFlag string, jsonOut, verbose bool) *Config {
	apiKey := strings.TrimSpace(apiKeyFlag)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv(EnvAPIKey))
	}

	baseURL := strings.TrimSpace(baseURLFlag)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv(EnvBaseURL))
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	// Normalize trailing slash so callers can join paths with leading slash
	// without worrying about double slashes.
	baseURL = strings.TrimRight(baseURL, "/")

	return &Config{
		APIKey:  apiKey,
		BaseURL: baseURL,
		JSON:    jsonOut,
		Verbose: verbose,
	}
}

// Validate enforces that an API key is present. Subcommands that hit the
// Observo API should call this before issuing requests.
func (c *Config) Validate() error {
	if c.APIKey == "" {
		return ErrMissingAPIKey
	}
	return nil
}
