package commands

import (
	"context"
	"github.com/gruntwork-io/go-commons/errors"
	"github.com/gruntwork-io/health-checker/server"
	"github.com/urfave/cli/v3"
)

// Create the CLI app with all commands (in this case a single one!), flags, and usage text configured.
func CreateCli(version string) *cli.Command {
	cmd := &cli.Command{
		Name:    "health-checker",
		Version: version,
		Usage:   "A simple HTTP server that will return 200 OK if the configured checks are all successful.",
		Commands: nil,
		Flags:    getDefaultFlags(),
		Action:   runHealthChecker,
		CustomHelpTemplate: ` NAME:
    {{.Name}} - {{.Usage}}

 USAGE:
    {{.HelpName}} {{if .Flags}}[options]{{end}}
    {{if .Commands}}
 OPTIONS:
    {{range .Flags}}{{.}}
    {{end}}{{end}}{{if .Copyright }}
 COPYRIGHT:
    {{.Copyright}}
    {{end}}{{if .Version}}
 VERSION:
    {{.Version}}
    {{end}}{{if len .Authors}}
 AUTHOR(S):
    {{range .Authors}}{{ . }}{{end}}
	{{end}}
`,
	}

	return cmd
}

func runHealthChecker(ctx context.Context, cliContext *cli.Command) error {
	if allCliOptionsEmpty(cliContext) {
		cli.ShowRootCommandHelpAndExit(cliContext, 0)
	}

	opts, err := parseOptions(cliContext)
	if isDebugMode() {
		opts.Logger.Infof("Note: To enable debug mode, set %s to \"true\"", ENV_VAR_NAME_DEBUG_MODE)
		return err
	}
	if err != nil {
		return errors.WithStackTrace(err)
	}
	if len(opts.Ports) > 0 {
		opts.Logger.Infof("The Health Check will attempt to connect to the following ports via TCP: %v", opts.Ports)
	}
	if len(opts.Scripts) > 0 {
		opts.Logger.Infof("The Health Check will attempt to run the following scripts: %v", opts.Scripts)
	}
	opts.Logger.Infof("Listening on Port %s...", opts.Listener)
	err = server.StartHttpServer(opts)
	if err != nil {
		return errors.WithStackTrace(err)
	}

	return nil
}
