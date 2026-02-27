package commands

import (
	"context"

	"github.com/gruntwork-io/go-commons/errors"
	"github.com/gruntwork-io/health-checker/server"
	"github.com/urfave/cli/v3"
)

// CreateCli initializes the root urfave/cli/v3 Command, parsing flags, metadata, and mapping the default action
// to the runHealthChecker method. It also customizes the help template to remove unused sections.
func CreateCli(version string) *cli.Command {
	app := &cli.Command{}

	app.CustomHelpTemplate = ` NAME:
    {{.Name}} - {{.Usage}}

 USAGE:
    {{.HelpName}} {{if .Flags}}[options]{{end}}
    {{if .Commands}}
 OPTIONS:
    {{range .VisibleFlags}}{{.}}
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
`

	app.Name = "health-checker"
	//	app.Author = "Gruntwork, Inc. <www.gruntwork.io> | https://github.com/gruntwork-io/health-checker"
	app.Version = version
	app.Usage = "A simple HTTP server that will return 200 OK if the configured checks are all successful."
	app.Commands = nil
	app.Flags = defaultFlags
	app.Action = runHealthChecker

	return app
}

func runHealthChecker(ctx context.Context, cmd *cli.Command) error {
	if allCliOptionsEmpty(cmd) {
		cli.ShowAppHelpAndExit(cmd, 0)
	}

	opts, err := parseOptions(cmd)
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
	if len(opts.HttpChecks) > 0 {
		var urls []string
		for _, check := range opts.HttpChecks {
			urls = append(urls, check.Url)
		}
		opts.Logger.Infof("The Health Check will attempt to connect to the following URLs via HTTP/S: %v", urls)
	}
	opts.Logger.Infof("Listening on Port %s...", opts.Listener)
	err = server.StartHttpServer(opts)
	if err != nil {
		return errors.WithStackTrace(err)
	}

	return nil
}
