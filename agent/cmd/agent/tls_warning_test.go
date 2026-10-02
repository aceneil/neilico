package main

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestInsecureSkipVerifyPrintsWarning(t *testing.T) {
	var stdout, stderr bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&stdout, nil))
	warnInsecureTLS(true, logger, &stderr)
	if !strings.Contains(stderr.String(), "SECURITY WARNING") || !strings.Contains(stdout.String(), "SECURITY WARNING") {
		t.Fatalf("missing prominent warning: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	warnInsecureTLS(false, logger, io.Discard)
	if stdout.Len() != 0 {
		t.Fatalf("secure configuration emitted warning: %q", stdout.String())
	}
}
