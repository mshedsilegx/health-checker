package commands

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/gruntwork-io/go-commons/errors"
	"github.com/gruntwork-io/health-checker/options"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"
)

const DEFAULT_LISTENER_IP_ADDRESS = "0.0.0.0"
const DEFAULT_LISTENER_PORT = 5500
const DEFAULT_TCP_TIMEOUT_SEC = 5
const DEFAULT_HTTP_TIMEOUT_SEC = 5
const DEFAULT_SCRIPT_TIMEOUT_SEC = 5
const ENV_VAR_NAME_DEBUG_MODE = "HEALTH_CHECKER_DEBUG"

func getDefaultFlags() []cli.Flag {
	return []cli.Flag{
		&cli.IntSliceFlag{
			Name:  "tcp-port",
			Usage: "[One of tcp-port/script Required] The port number on which a TCP connection will be attempted. Specify one or more times. Example: 8000",
		},
		&cli.StringFlag{
			Name:  "tcp-port-range",
			Usage: "[One of tcp-port/script Required] A range of port numbers on which a TCP connection will be attempted. Specify a comma-separated list (e.g., 80,8080) or a range (e.g., 8000-8005).",
		},
		&cli.IntSliceFlag{
			Name:  "http-port",
			Usage: "[One of http-port/script Required] The port number on which an HTTP connection will be attempted. Specify one or more times. Example: 8000",
		},
		&cli.StringFlag{
			Name:  "http-port-range",
			Usage: "[One of http-port/script Required] A range of port numbers on which an HTTP connection will be attempted. Specify a comma-separated list (e.g., 80,8080) or a range (e.g., 8000-8005).",
		},
		&cli.StringFlag{
			Name:  "http-url",
			Usage: "[One of http-url/script Required] A URL on which an HTTP connection will be attempted.",
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
			Name:  "http-timeout",
			Usage: "[Optional] Timeout, in seconds, to wait for the HTTP connections to complete. Example: 10",
			Value: DEFAULT_HTTP_TIMEOUT_SEC,
		},
		&cli.StringFlag{
			Name:  "http-match",
			Usage: "[Optional] A string or regexp to search for in the HTTP response. If the string is found, the check is successful.",
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
		&cli.BoolFlag{
			Name:  "msg",
			Usage: "[Optional] Return a JSON object with a detailed status and message instead of plain text.",
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
		return nil, errors.WithStackTrace(InvalidLogLevel(logLevel))
	}
	logger.SetLevel(level)

	ports := cliContext.Value("tcp-port").([]int)
	portRange := cliContext.Value("tcp-port-range").(string)
	rangePorts, err := parsePortRange(portRange)
	if err != nil {
		return nil, errors.WithStackTrace(err)
	}
	ports = append(ports, rangePorts...)

	httpPorts := cliContext.Value("http-port").([]int)
	httpPortRange := cliContext.Value("http-port-range").(string)
	httpRangePorts, err := parsePortRange(httpPortRange)
	if err != nil {
		return nil, errors.WithStackTrace(err)
	}
	httpPorts = append(httpPorts, httpRangePorts...)
	httpUrl := cliContext.Value("http-url").(string)
	if err := validateHttpUrl(httpUrl); err != nil {
		return nil, errors.WithStackTrace(err)
	}

	scriptArr := cliContext.Value("script").([]string)
	scripts, err := options.ParseScripts(scriptArr)
	if err != nil {
		return nil, errors.WithStackTrace(err)
	}

	if len(ports) == 0 && len(httpPorts) == 0 && httpUrl == "" && len(scripts) == 0 {
		return nil, errors.WithStackTrace(OneOfParamsRequired{Params: []string{"tcp-port", "http-port", "http-url", "script"}})
	}

	singleflight := cliContext.Value("singleflight").(bool)

	returnJson := cliContext.Value("msg").(bool)

	tcpTimeout := cliContext.Value("tcp-timeout").(int)
	httpTimeout := cliContext.Value("http-timeout").(int)
	httpMatch := cliContext.Value("http-match").(string)
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
		HttpPorts:     httpPorts,
		HttpUrl:       httpUrl,
		Scripts:       scripts,
		TcpTimeout:    tcpTimeout,
		HttpTimeout:   httpTimeout,
		HttpMatch:     httpMatch,
		ScriptTimeout: scriptTimeout,
		Singleflight:  singleflight,
		ReturnJson:    returnJson,
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
	Params []string
}

func (paramNames OneOfParamsRequired) Error() string {
	paramStrings := []string{}
	for _, param := range paramNames.Params {
		paramStrings = append(paramStrings, fmt.Sprintf("--%s", param))
	}
	return fmt.Sprintf("Missing required parameter, one of %s is required", strings.Join(paramStrings, " / "))
}

func validateHttpUrl(httpUrl string) error {
	if httpUrl == "" {
		return nil
	}

	parsedUrl, err := url.Parse(httpUrl)
	if err != nil {
		return err
	}

	ips, err := net.LookupIP(parsedUrl.Hostname())
	if err != nil {
		return err
	}

	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() {
			return fmt.Errorf("URL points to a local or private IP address: %s", httpUrl)
		}
	}

	return nil
}

func parsePortRange(portRange string) ([]int, error) {
	if portRange == "" {
		return nil, nil
	}

	var ports []int
	parts := strings.Split(portRange, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				return nil, fmt.Errorf("invalid port range: %s", part)
			}

			start, err := strconv.Atoi(rangeParts[0])
			if err != nil {
				return nil, fmt.Errorf("invalid port number: %s", rangeParts[0])
			}

			end, err := strconv.Atoi(rangeParts[1])
			if err != nil {
				return nil, fmt.Errorf("invalid port number: %s", rangeParts[1])
			}

			if start > end {
				return nil, fmt.Errorf("invalid port range: %d-%d", start, end)
			}

			for i := start; i <= end; i++ {
				ports = append(ports, i)
			}
		} else {
			port, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid port number: %s", part)
			}
			ports = append(ports, port)
		}
	}

	return ports, nil
}
