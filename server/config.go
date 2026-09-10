package main

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// config is everything the server is told. Environment only: this runs in a
// container behind a deploy, where a flag is something you cannot change
// without a new command line and a file is something you have to get onto the
// image.
type config struct {
	databaseURL string
	addr        string
	// signupToken gates workspace creation. Empty leaves the endpoint closed,
	// which is what a server that already has its workspace wants.
	signupToken string
	logLevel    slog.Level
}

func load() (config, error) {
	cfg := config{
		databaseURL: os.Getenv("DATABASE_URL"),
		addr:        ":" + cmp.Or(os.Getenv("PORT"), "8080"),
		signupToken: os.Getenv("WORK_SIGNUP_TOKEN"),
	}
	if cfg.databaseURL == "" {
		return config{}, errors.New("DATABASE_URL is not set")
	}

	level := cmp.Or(os.Getenv("WORK_LOG_LEVEL"), "info")
	if err := cfg.logLevel.UnmarshalText([]byte(level)); err != nil {
		return config{}, fmt.Errorf("WORK_LOG_LEVEL %q is not one of debug, info, warn, error", level)
	}

	// A short signup token is worse than none: it leaves creation open to
	// guessing while looking like it is closed.
	if cfg.signupToken != "" && len(strings.TrimSpace(cfg.signupToken)) < 24 {
		return config{}, errors.New("WORK_SIGNUP_TOKEN is under 24 characters; leave it unset to close signup instead")
	}
	return cfg, nil
}
