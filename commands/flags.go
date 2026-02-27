package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/gruntwork-io/go-commons/logging"
	"github.com/gruntwork-io/health-checker/options"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"
)

const DEFAULT_LISTENER_IP_ADDRESS = "0.0.0.0"
const DEFAULT_LISTENER_PORT = 5500
const DEFAULT_SCRIPT_TIMEOUT_SEC = 5
const ENV_VAR_NAME_DEBUG_MODE = "HEALTH_CHECKER_DEBUG"

var portFlag = &cli.IntSliceFlag{
	Name:  "port",
	Usage: "[One of port/script Required] The port number on which a TCP connection will be attempted. Specify one or more times. Example: 8000",
}

var scriptFlag = &cli.StringSliceFlag{
	Name:  "script",
	Usage: "[One of port/script/http Required] The path to script that will be run. Specify one or more times. Example: \"/usr/local/bin/health-check.sh --http-port 8000\"",
}

var httpCheckFlag = &cli.StringSliceFlag{
	Name:  "http",
	Usage: "[One of port/script/http Required] An HTTP(S) URL to probe. The check succeeds if it returns a 2xx status code. Specify one or more times. Example: \"https://localhost:8080/health\"",
}

var httpCheckVerifyPayloadFlag = &cli.StringSliceFlag{
	Name:  "verify-payload",
	Usage: "[Optional] A regular expression to match against the body of the HTTP(S) checks. If specified, the check only succeeds if the status code is 2xx AND the response body matches the regex. Must be specified exactly once per --http flag if used. Example: \"ready\"",
}

var allowInsecureTlsFlag = &cli.BoolFlag{
	Name:  "allow-insecure-tls",
	Usage: "[Optional] Skip TLS certificate verification for HTTPS checks. Use this if you are probing endpoints with self-signed certificates or broken trust chains.",
}

var scriptTimeoutFlag = &cli.IntFlag{
	Name:  "script-timeout",
	Usage: "[Optional] Timeout, in seconds, to wait for the scripts to complete. Example: 10",
	Value: DEFAULT_SCRIPT_TIMEOUT_SEC,
}

var httpReadTimeoutFlag = &cli.IntFlag{
	Name:  "http-read-timeout",
	Usage: "[Optional] Timeout, in seconds, for reading the entire HTTP request, including the body. Example: 5",
	Value: 5,
}

var httpWriteTimeoutFlag = &cli.IntFlag{
	Name:  "http-write-timeout",
	Usage: "[Optional] Timeout, in seconds, for writing the HTTP response. Dynamically scales with script timeout + 5 if set to 0. Example: 15",
	Value: 0,
}

var httpIdleTimeoutFlag = &cli.IntFlag{
	Name:  "http-idle-timeout",
	Usage: "[Optional] Timeout, in seconds, to wait for the next request when keep-alives are enabled. Example: 15",
	Value: 15,
}

var tcpDialTimeoutFlag = &cli.IntFlag{
	Name:  "tcp-dial-timeout",
	Usage: "[Optional] Timeout, in seconds, for dialing TCP connections for health checks. Example: 5",
	Value: 5,
}

var singleflightFlag = &cli.BoolFlag{
	Name:  "singleflight",
	Usage: "[Optional] Enable singleflight mode, which makes concurrent requests share the same check.",
}

var detailedStatusFlag = &cli.BoolFlag{
	Name:  "detailed-status",
	Usage: "[Optional] Return a detailed JSON payload indicating elapsed time and specific error messages if probes fail.",
}

var listenerFlag = &cli.StringFlag{
	Name:  "listener",
	Usage: "[Optional] The IP address and port on which inbound HTTP connections will be accepted.",
	Value: fmt.Sprintf("%s:%d", DEFAULT_LISTENER_IP_ADDRESS, DEFAULT_LISTENER_PORT),
}

var logLevelFlag = &cli.StringFlag{
	Name:  "log-level",
	Usage: fmt.Sprintf("[Optional] Set the log level to `LEVEL`. Must be one of: %v", logrus.AllLevels),
	Value: logrus.InfoLevel.String(),
}

var defaultFlags = []cli.Flag{
	portFlag,
	scriptFlag,
	httpCheckFlag,
	httpCheckVerifyPayloadFlag,
	allowInsecureTlsFlag,
	scriptTimeoutFlag,
	detailedStatusFlag,
	httpReadTimeoutFlag,
	httpWriteTimeoutFlag,
	httpIdleTimeoutFlag,
	tcpDialTimeoutFlag,
	singleflightFlag,
	listenerFlag,
	logLevelFlag,
}

// Return true if no options at all were passed to the CLI. Note that we are specifically testing for flags, some of which
// are required, not just args.
func allCliOptionsEmpty(cmd *cli.Command) bool {
	return cmd.NumFlags() == 0
}

// parseOptions processes the user-provided CLI arguments from the urfave/cli/v3 Context.
// It maps these inputs to the internal Options struct, configuring loggers, translating
// string slices into domain objects (like Scripts), and validating that at least one
// check strategy (port or script) was requested.
func parseOptions(cmd *cli.Command) (*options.Options, error) {
	logger := logging.GetLogger("health-checker", "v0.0.0")

	// By default logrus logs to stderr. But since most output in this tool is informational, we default to stdout.
	logger.Logger.Out = os.Stdout

	logLevel := cmd.String(logLevelFlag.Name)
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		return nil, InvalidLogLevel(logLevel)
	}
	logger.Logger.SetLevel(level)

	ports := make([]int, 0)
	for _, p := range cmd.IntSlice("port") {
		ports = append(ports, int(p))
	}

	scriptArr := cmd.StringSlice("script")
	scripts, err := options.ParseScripts(scriptArr)
	if err != nil {
		return nil, err
	}

	httpArr := cmd.StringSlice("http")
	verifyPayloads := cmd.StringSlice("verify-payload")

	var httpChecks []options.HttpCheck
	if len(verifyPayloads) > 0 && len(verifyPayloads) != len(httpArr) {
		return nil, fmt.Errorf("if --verify-payload is specified, it must be specified exactly once per --http flag")
	}

	for i, url := range httpArr {
		verifyPayload := ""
		if len(verifyPayloads) > i {
			verifyPayload = verifyPayloads[i]
		}
		httpChecks = append(httpChecks, options.HttpCheck{
			Url:           url,
			VerifyPayload: verifyPayload,
		})
	}

	if len(ports) == 0 && len(scripts) == 0 && len(httpChecks) == 0 {
		return nil, OneOfParamsRequired{portFlag.Name, scriptFlag.Name, httpCheckFlag.Name}
	}

	singleflight := cmd.Bool("singleflight")
	detailedStatus := cmd.Bool("detailed-status")
	allowInsecureTls := cmd.Bool("allow-insecure-tls")

	scriptTimeout := int(cmd.Int("script-timeout"))
	httpReadTimeout := int(cmd.Int("http-read-timeout"))
	httpWriteTimeout := int(cmd.Int("http-write-timeout"))
	httpIdleTimeout := int(cmd.Int("http-idle-timeout"))
	tcpDialTimeout := int(cmd.Int("tcp-dial-timeout"))

	listener := cmd.String("listener")
	if listener == "" {
		return nil, MissingParam(listenerFlag.Name)
	}

	return &options.Options{
		Ports:            ports,
		Scripts:          scripts,
		HttpChecks:       httpChecks,
		ScriptTimeout:    scriptTimeout,
		HttpReadTimeout:  httpReadTimeout,
		HttpWriteTimeout: httpWriteTimeout,
		HttpIdleTimeout:  httpIdleTimeout,
		TcpDialTimeout:   tcpDialTimeout,
		Singleflight:     singleflight,
		DetailedStatus:   detailedStatus,
		AllowInsecureTLS: allowInsecureTls,
		Listener:         listener,
		Logger:           logger.Logger,
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
	param3 string
}

func (paramNames OneOfParamsRequired) Error() string {
	return fmt.Sprintf("Missing required parameter, one of --%s / --%s / --%s required", string(paramNames.param1), string(paramNames.param2), string(paramNames.param3))
}
