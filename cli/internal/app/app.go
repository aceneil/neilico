package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"neilico/cli/internal/api"
	"neilico/cli/internal/config"
)

const helpText = `neilicoctl - NEILICO control-plane CLI

Usage:
  neilicoctl [global options] <command> [command options]

Commands:
  login                 Authenticate with password or API token
  token list            List API tokens (masked)
  token create          Create an API token (shown once)
  token revoke          Revoke an API token
  node list             List nodes
  node register         Register a node
  node mtls             Issue or renew a node client certificate
  node trust-ca         Save the control-plane CA certificate
  network create        Create a virtual network
  network join          Join a node to a virtual network
  domain add            Add a domain and proxy rule
  status                Show control-plane status
  agent config          Fetch and print an agent configuration

Global options:
  --server URL          Control-plane URL (also NEILICO_SERVER)
  --config PATH         Credential file (default ~/.neilicoctl/config.yaml)
  --help                Show this help
`

type App struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func New(stdin io.Reader, stdout, stderr io.Writer) *App {
	return &App{Stdin: stdin, Stdout: stdout, Stderr: stderr}
}

func (a *App) Run(ctx context.Context, args []string) error {
	args, serverOverride, configPath, help := extractGlobal(args)
	if help || len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(a.Stdout, helpText)
		return nil
	}
	credentials, err := config.Load(configPath, serverOverride)
	if err != nil {
		return err
	}
	client, err := api.NewWithTLS(credentials.Server, credentials.AccessToken, api.TLSOptions{
		CAFile:         credentials.CAFile,
		ClientCertFile: credentials.ClientCertFile,
		ClientKeyFile:  credentials.ClientKeyFile,
	})
	if err != nil {
		return err
	}
	command := args[0]
	rest := args[1:]
	switch command {
	case "login":
		return a.login(ctx, &credentials, configPath, rest)
	case "token":
		return a.token(ctx, client, rest)
	case "node":
		return a.node(ctx, client, &credentials, configPath, rest)
	case "network":
		return a.network(ctx, client, &credentials, rest)
	case "domain":
		return a.domain(ctx, client, rest)
	case "status":
		return a.status(ctx, client)
	case "agent":
		return a.agent(ctx, client, rest)
	default:
		return fmt.Errorf("unknown command %q; run neilicoctl --help", command)
	}
}

func extractGlobal(args []string) ([]string, string, string, bool) {
	result := make([]string, 0, len(args))
	server := ""
	path := ""
	help := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--help" || arg == "-h":
			help = true
		case arg == "--server" && index+1 < len(args):
			index++
			server = args[index]
		case strings.HasPrefix(arg, "--server="):
			server = strings.TrimPrefix(arg, "--server=")
		case arg == "--config" && index+1 < len(args):
			index++
			path = args[index]
		case strings.HasPrefix(arg, "--config="):
			path = strings.TrimPrefix(arg, "--config=")
		default:
			result = append(result, arg)
		}
	}
	return result, server, path, help
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr)
	return set
}

func requireFlag(value, name string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("--%s is required", name)
	}
	return nil
}

func errHelp(set *flag.FlagSet) error {
	return errors.New(set.Name() + ": invalid arguments; use --help")
}
