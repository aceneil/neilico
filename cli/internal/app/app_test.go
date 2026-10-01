package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestHelpListsAllCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := New(strings.NewReader(""), &stdout, &stderr).Run(context.Background(), []string{"--help"})
	if err != nil {
		t.Fatal(err)
	}
	text := stdout.String()
	for _, command := range []string{"login", "node list", "node register", "network create", "network join", "domain add", "status", "agent config"} {
		if !strings.Contains(text, command) {
			t.Fatalf("help omitted %q:\n%s", command, text)
		}
	}
}

func TestGlobalServerOverrideAndArgumentParsing(t *testing.T) {
	args, server, path, help := extractGlobal([]string{"--server", "https://cli.example.test", "--config=/tmp/cli.yaml", "node", "register", "--name=node"})
	if help || server != "https://cli.example.test" || path != "/tmp/cli.yaml" {
		t.Fatalf("global parse = %v %q %q %v", args, server, path, help)
	}
	if strings.Join(args, " ") != "node register --name=node" {
		t.Fatalf("remaining args = %#v", args)
	}
}

func TestRequiredFlagsFailWithoutNetworkCalls(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := New(strings.NewReader(""), &stdout, &stderr).Run(context.Background(), []string{"node", "register"})
	if err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Fatalf("node register error = %v", err)
	}
	err = New(strings.NewReader(""), &stdout, &stderr).Run(context.Background(), []string{"network", "join"})
	if err == nil || !strings.Contains(err.Error(), "--network is required") {
		t.Fatalf("network join error = %v", err)
	}
}
