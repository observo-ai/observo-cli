package config

import (
	"errors"
	"testing"
)

func TestResolve_PrecedenceFlagOverEnvOverDefault(t *testing.T) {
	t.Setenv(EnvAPIKey, "from-env")
	t.Setenv(EnvBaseURL, "https://from-env.observoai.co")

	t.Run("flag wins over env", func(t *testing.T) {
		c := Resolve("from-flag", "https://from-flag", false, false)
		if c.APIKey != "from-flag" {
			t.Fatalf("APIKey: want from-flag, got %q", c.APIKey)
		}
		if c.BaseURL != "https://from-flag" {
			t.Fatalf("BaseURL: want from-flag, got %q", c.BaseURL)
		}
	})

	t.Run("env wins over default", func(t *testing.T) {
		c := Resolve("", "", false, false)
		if c.APIKey != "from-env" {
			t.Fatalf("APIKey: want from-env, got %q", c.APIKey)
		}
		if c.BaseURL != "https://from-env.observoai.co" {
			t.Fatalf("BaseURL: want from-env, got %q", c.BaseURL)
		}
	})
}

func TestResolve_DefaultBaseURLWhenNoFlagNoEnv(t *testing.T) {
	// Explicitly clear env to defeat any inherited test order leakage.
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")

	c := Resolve("", "", false, false)
	if c.BaseURL != DefaultBaseURL {
		t.Fatalf("BaseURL: want %q, got %q", DefaultBaseURL, c.BaseURL)
	}
	if c.APIKey != "" {
		t.Fatalf("APIKey: want empty, got %q", c.APIKey)
	}
}

func TestResolve_TrimsWhitespaceAndTrailingSlash(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")

	c := Resolve("  key-with-spaces  ", "https://example.com/api/", false, false)
	if c.APIKey != "key-with-spaces" {
		t.Fatalf("APIKey: trimming failed, got %q", c.APIKey)
	}
	if c.BaseURL != "https://example.com/api" {
		t.Fatalf("BaseURL: trailing slash strip failed, got %q", c.BaseURL)
	}
}

func TestValidate_ReturnsErrMissingAPIKeyWhenEmpty(t *testing.T) {
	c := &Config{APIKey: ""}
	err := c.Validate()
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Fatalf("want ErrMissingAPIKey, got %v", err)
	}
}

func TestValidate_PassesWhenAPIKeySet(t *testing.T) {
	c := &Config{APIKey: "anything"}
	if err := c.Validate(); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}
