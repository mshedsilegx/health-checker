package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/gruntwork-io/health-checker/options"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"
)

const DEFAULT_LISTENER_IP_ADDRESS = "0.0.0.0"
const DEFAULT_LISTENER_PORT = 5500
const DEFAULT_TCP_TIMEOUT_SEC = 5
const DEFAULT_SCRIPT_TIMEOUT_SEC = 5
const ENV_VAR_NAME_DEBUG_MODE = "HEALTH_CHECKER_DEBUG"

func getDefaultFlags() []cli.Flag {
	return []cli.Flag{
		&cli.IntSliceFlag{
			Name:  "port",
			Usage: "[One of port/script Required] The port number on which a TCP connection will be attempted. Specify one or more times. Example: 8000",
		},
		&cli.StringSliceFlag{
			Name:  "script",
			Usage: "[One of port/script Required] The path to script that will be run. Specify one or more times. Example: \"/usr/local/bin/health-check.sh --http-port 8000\"",
		},
		&cli.IntFlag{
			Name:  "tcp-timeout",
			Usage: "[Optional] Timeout, in seconds, to wait for the TCP connections to complete. Example: 10",
			Value: DEFAULT_TCP_TIMEOUT_SEC,
		},
		&cli.IntFlag{
			Name:  "script-timeout",
			Usage: "[Optional] Timeout, in seconds, to wait for the scripts to complete. Example: 10",
			Value: DEFAULT_SCRIPT_TIMEOUT_SEC,
		},
		&cli.BoolFlag{
			Name:  "singleflight",
			Usage: "[Optional] Enable singleflight mode, which makes concurrent requests share the same check.",
		},
		&cli.StringFlag{
			Name:  "listener",
			Usage: "[Optional] The IP address and port on which inbound HTTP connections will be accepted.",
			Value: fmt.Sprintf("%s:%d", DEFAULT_LISTENER_IP_ADDRESS, DEFAULT_LISTENER_PORT),
		},
		&cli.StringFlag{
			Name:  "log-level",
			Usage: fmt.Sprintf("[Optional] Set the log level to `LEVEL`. Must be one of: %v", logrus.AllLevels),
			Value: logrus.InfoLevel.String(),
		},
	}
}

// Return true if no options at all were passed to the CLI. Note that we are specifically testing for flags, some of which
// are required, not just args.
func allCliOptionsEmpty(cliContext *cli.Command) bool {
	return cliContext.NumFlags() == 0
}

// Parse and validate all CLI options
func parseOptions(cliContext *cli.Command) (*options.Options, error) {
	logger := logrus.New()

	// By default logrus logs to stderr. But since most output in this tool is informational, we default to stdout.
	logger.Out = os.Stdout

	logLevel := cliContext.Value("log-level").(string)
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		return nil, InvalidLogLevel(logLevel)
	}
	logger.SetLevel(level)

	ports := cliContext.Value("port").([]int)

	scriptArr := cliContext.Value("script").([]string)
	scripts := options.ParseScripts(scriptArr)

	if len(ports) == 0 && len(scripts) == 0 {
		return nil, OneOfParamsRequired{"port", "script"}
	}

	singleflight := cliContext.Value("singleflight").(bool)

	tcpTimeout := cliContext.Value("tcp-timeout").(int)
	scriptTimeout := cliContext.Value("script-timeout").(int)

	var listener string
	if cliContext.IsSet("listener") {
		listener = cliContext.Value("listener").(string)
		if !strings.Contains(listener, ":") {
			listener = fmt.Sprintf("%s:%s", DEFAULT_LISTENER_IP_ADDRESS, listener)
		}
	} else {
		listener = fmt.Sprintf("%s:%d", DEFAULT_LISTENER_IP_ADDRESS, DEFAULT_LISTENER_PORT)
	}

	if listener == "" {
		return nil, MissingParam("listener")
	}

	return &options.Options{
		Ports:         ports,
		Scripts:       scripts,
		TcpTimeout:    tcpTimeout,
		ScriptTimeout: scriptTimeout,
		Singleflight:  singleflight,
		Listener:      listener,
		Logger:        logger,
	}, nil
}

// Some error types are simple enough that we'd rather just show the error message directly instead of vomiting out a
// whole stack trace in log output. Therefore, allow a debug mode that always shows full stack traces. Otherwise, show
// simple messages.
func isDebugMode() bool {
	envVar, _ := os.LookupEnv(ENV_VAR_NAME_DEBUG_MODE)
	envVar = strings.ToLower(envVar)
	return envVar == "true"
}

// Custom error types

type InvalidLogLevel string

func (invalidLogLevel InvalidLogLevel) Error() string {
	return fmt.Sprintf("The log-level value \"%s\" is invalid", string(invalidLogLevel))
}

type MissingParam string

func (paramName MissingParam) Error() string {
	return fmt.Sprintf("Missing required parameter --%s", string(paramName))
}

type OneOfParamsRequired struct {
	param1 string
	param2 string
}

func (paramNames OneOfParamsRequired) Error() string {
	return fmt.Sprintf("Missing required parameter, one of --%s / --%s required", string(paramNames.param1), string(paramNames.param2))
}
