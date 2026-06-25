package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestVersionRequested(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"version"}} {
		if !versionRequested(args) {
			t.Fatalf("versionRequested(%q) = false", args)
		}
	}
	for _, args := range [][]string{nil, {}, {"--help"}, {"--version", "extra"}} {
		if versionRequested(args) {
			t.Fatalf("versionRequested(%q) = true", args)
		}
	}
}

func TestLoggerCanBeDirectedAwayFromStdout(t *testing.T) {
	var stderr bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&stderr, nil))
	logger.Info("stdio-safe")
	if !strings.Contains(stderr.String(), "stdio-safe") {
		t.Fatalf("logger did not write to the configured stderr sink: %q", stderr.String())
	}
}
