package config

import (
	"os"
	"strings"
)

// Config holds process-level settings used for wiring the service.
type Config struct {
	Port string
}

// Load reads configuration from environment variables with safe defaults.
func Load() Config {
	port := os.Getenv("KVSTORE_PORT")
	if port == "" {
		port = os.Getenv("PORT")
	}
	if port == "" {
		port = "8080"
	}

	return Config{Port: port}
}

// Addr returns an http listen address, ensuring it includes ":" prefix.
func (c Config) Addr() string {
	if strings.HasPrefix(c.Port, ":") {
		return c.Port
	}
	return ":" + c.Port
}
