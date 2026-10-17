package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

type playConfig struct {
	packageName    string
	audience       string
	serviceAccount string
	allowTest      bool
}

// playConfigFromEnv reads the Google Play billing settings. It returns nil
// when PLAY_PACKAGE_NAME is unset, which disables Play billing.
func playConfigFromEnv() (*playConfig, error) {
	packageName := os.Getenv("PLAY_PACKAGE_NAME")
	if packageName == "" {
		return nil, nil
	}
	cfg := &playConfig{
		packageName:    packageName,
		audience:       os.Getenv("PLAY_RTDN_AUDIENCE"),
		serviceAccount: os.Getenv("PLAY_RTDN_SERVICE_ACCOUNT"),
	}
	if cfg.audience == "" {
		return nil, errors.New("PLAY_RTDN_AUDIENCE is required with PLAY_PACKAGE_NAME")
	}
	if cfg.serviceAccount == "" {
		return nil, errors.New("PLAY_RTDN_SERVICE_ACCOUNT is required with PLAY_PACKAGE_NAME")
	}
	if raw := os.Getenv("PLAY_ALLOW_TEST_PURCHASES"); raw != "" {
		allow, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid PLAY_ALLOW_TEST_PURCHASES %q: %w", raw, err)
		}
		cfg.allowTest = allow
	}
	return cfg, nil
}
