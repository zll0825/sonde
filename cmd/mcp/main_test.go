package main

import (
	"flag"
	"strings"
	"testing"
)

func TestUsageMentionsFlagsAndEnv(t *testing.T) {
	if !strings.Contains(usageText, "-http") {
		t.Error("usage missing -http flag")
	}
	if !strings.Contains(usageText, "DATABASE_URL") {
		t.Error("usage missing DATABASE_URL env")
	}
	if !strings.Contains(usageText, "API_TOKEN") {
		t.Error("usage missing API_TOKEN env")
	}
}

func TestFlagsParsing(t *testing.T) {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	httpAddr := fs.String("http", "", "HTTP address")

	if err := fs.Parse([]string{"-http", "127.0.0.1:8081"}); err != nil {
		t.Fatalf("unexpected error parsing flags: %v", err)
	}

	if *httpAddr != "127.0.0.1:8081" {
		t.Errorf("expected httpAddr to be 127.0.0.1:8081, got %s", *httpAddr)
	}
}
