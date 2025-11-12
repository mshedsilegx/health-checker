package commands

import (
	"context"
	"github.com/gruntwork-io/health-checker/options"
	"github.com/gruntwork-io/health-checker/test"
	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v3"
	"strings"
	"testing"
)

func TestParseChecksFromConfig(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		args            []string
		expectedOptions *options.Options
		expectedErr     string
	}{
		{
			"no options",
			[]string{},
			nil,
			"Missing required parameter, one of",
		},
		{
			"invalid log-level",
			[]string{"--log-level", "notreally"},
			nil,
			"The log-level value",
		},
		{
			"invalid listener",
			[]string{"--listener"},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{}, defaultListener(), []int{8080}),
			"Missing required parameter, one of",
		},
		{
			"valid listener",
			[]string{"--listener", "1234", "--tcp-port", "4321"},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{}, test.ListenerString(DEFAULT_LISTENER_IP_ADDRESS, 1234), []int{4321}),
			"",
		},
		{
			"single port",
			[]string{"--tcp-port", "8080"},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{}, defaultListener(), []int{8080}),
			"",
		},
		{
			"multiple ports",
			[]string{"--tcp-port", "8080", "--tcp-port", "8081"},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{}, defaultListener(), []int{8080, 8081}),
			"",
		},
		{
			"both port and script",
			[]string{"--tcp-port", "8080", "--script", "\"/usr/local/bin/check.sh 1234\""},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{"\"/usr/local/bin/check.sh 1234\""}, defaultListener(), []int{8080}),
			"",
		},
		{
			"single script",
			[]string{"--script", "/usr/local/bin/check.sh"},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{"/usr/local/bin/check.sh"}, defaultListener(), []int{}),
			"",
		},
		{
			"single script with custom timeout",
			[]string{"--script", "/usr/local/bin/check.sh", "--script-timeout", "11"},
			createOptionsForTest(t, 11, []string{"/usr/local/bin/check.sh"}, defaultListener(), []int{}),
			"",
		},
		{
			"multiple scripts",
			[]string{"--script", "/usr/local/bin/check1.sh", "--script", "/usr/local/bin/check2.sh"},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{"/usr/local/bin/check1.sh", "/usr/local/bin/check2.sh"}, defaultListener(), []int{}),
			"",
		},
		{
			"tcp port range",
			[]string{"--tcp-port-range", "8000-8002"},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{}, defaultListener(), []int{8000, 8001, 8002}),
			"",
		},
		{
			"http port",
			[]string{"--http-port", "8080"},
			createOptionsForTestWithHttp(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{}, defaultListener(), []int{}, []int{8080}, ""),
			"",
		},
		{
			"http port range",
			[]string{"--http-port-range", "8000-8002"},
			createOptionsForTestWithHttp(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{}, defaultListener(), []int{}, []int{8000, 8001, 8002}, ""),
			"",
		},
		{
			"http url",
			[]string{"--http-url", "http://gruntwork.io"},
			createOptionsForTestWithHttp(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{}, defaultListener(), []int{}, []int{}, "http://gruntwork.io"),
			"",
		},
		{
			"http url local",
			[]string{"--http-url", "http://localhost:8080"},
			nil,
			"URL points to a local or private IP address",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			context := createContextForTesting(testCase.args)

			actualOptions, actualErr := parseOptions(context)

			if testCase.expectedErr != "" {
				if actualErr == nil {
					assert.FailNow(t, "Expected error %v but got nothing.", testCase.expectedErr)
				}
				assert.True(t, strings.Contains(actualErr.Error(), testCase.expectedErr), "Expected error %v but got error %v", testCase.expectedErr, actualErr)
			} else {
				assert.Nil(t, actualErr, "Unexpected error: %v", actualErr)
				assertOptionsEqual(t, *testCase.expectedOptions, *actualOptions, "For args %v", testCase.args)
			}
		})
	}

}

func defaultListener() string {
	return test.ListenerString(DEFAULT_LISTENER_IP_ADDRESS, DEFAULT_LISTENER_PORT)
}

func assertOptionsEqual(t *testing.T, expected options.Options, actual options.Options, msgAndArgs ...interface{}) {
	assert.Equal(t, expected.ScriptTimeout, actual.ScriptTimeout, msgAndArgs...)
	assert.ElementsMatch(t, expected.Scripts, actual.Scripts, msgAndArgs...)
	assert.Equal(t, expected.Listener, actual.Listener, msgAndArgs...)
	assert.ElementsMatch(t, expected.Ports, actual.Ports, msgAndArgs...)
	assert.ElementsMatch(t, expected.HttpPorts, actual.HttpPorts, msgAndArgs...)
	assert.Equal(t, expected.HttpUrl, actual.HttpUrl, msgAndArgs...)
}

func createContextForTesting(args []string) *cli.Command {
	c := CreateCli("0.0.0")
	c.Action = func(ctx context.Context, cmd *cli.Command) error {
		return nil
	}
	_ = c.Run(context.Background(), append([]string{"health-checker"}, args...))
	return c
}

func createOptionsForTest(t *testing.T, scriptTimeout int, scripts []string, listener string, ports []int) *options.Options {
	opts := &options.Options{}
	opts.ScriptTimeout = scriptTimeout
	parsedScripts, err := options.ParseScripts(scripts)
	assert.Nil(t, err, "Unexpected error: %v", err)
	opts.Scripts = parsedScripts
	opts.Listener = listener
	opts.Ports = ports
	return opts
}

func createOptionsForTestWithHttp(t *testing.T, scriptTimeout int, scripts []string, listener string, ports []int, httpPorts []int, httpUrl string) *options.Options {
	opts := createOptionsForTest(t, scriptTimeout, scripts, listener, ports)
	opts.HttpPorts = httpPorts
	opts.HttpUrl = httpUrl
	return opts
}
