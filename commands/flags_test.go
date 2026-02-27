package commands

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gruntwork-io/health-checker/options"
	"github.com/gruntwork-io/health-checker/test"
	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v3"
)

func createDummyScript(t *testing.T, dir string, name string, content string) string {
	scriptPath := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		scriptPath += ".bat"
	} else {
		scriptPath += ".sh"
	}
	err := os.WriteFile(scriptPath, []byte(content), 0755)
	if err != nil {
		t.Fatalf("Failed to create dummy script: %v", err)
	}
	return filepath.ToSlash(scriptPath)
}

func TestParseChecksFromConfig(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	dummyScript := createDummyScript(t, tmpDir, "check", "echo ok")
	dummyScript1 := createDummyScript(t, tmpDir, "check1", "echo ok")
	dummyScript2 := createDummyScript(t, tmpDir, "check2", "echo ok")

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
			"flag needs an argument: --listener",
		},
		{
			"valid listener",
			[]string{"--listener", test.ListenerString(DEFAULT_LISTENER_IP_ADDRESS, 1234), "--port", "4321"},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{}, test.ListenerString(DEFAULT_LISTENER_IP_ADDRESS, 1234), []int{4321}),
			"",
		},
		{
			"single port",
			[]string{"--port", "8080"},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{}, defaultListener(), []int{8080}),
			"",
		},
		{
			"multiple ports",
			[]string{"--port", "8080", "--port", "8081"},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{}, defaultListener(), []int{8080, 8081}),
			"",
		},
		{
			"both port and script",
			[]string{"--port", "8080", "--script", dummyScript + " 1234"},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{dummyScript + " 1234"}, defaultListener(), []int{8080}),
			"",
		},
		{
			"single script",
			[]string{"--script", dummyScript},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{dummyScript}, defaultListener(), []int{}),
			"",
		},
		{
			"single script with custom timeout",
			[]string{"--script", dummyScript, "--script-timeout", "11"},
			createOptionsForTest(t, 11, []string{dummyScript}, defaultListener(), []int{}),
			"",
		},
		{
			"multiple scripts",
			[]string{"--script", dummyScript1, "--script", dummyScript2},
			createOptionsForTest(t, DEFAULT_SCRIPT_TIMEOUT_SEC, []string{dummyScript1, dummyScript2}, defaultListener(), []int{}),
			"",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {

			var actualOptions *options.Options
			var actualErr error

			app := CreateCli("0.0.0")
			app.Action = func(ctx context.Context, cmd *cli.Command) error {
				actualOptions, actualErr = parseOptions(cmd)
				return nil
			}

			// Prepend a dummy program name to the args, as urfave/cli expects os.Args[0] to be the program name
			args := append([]string{"health-checker"}, testCase.args...)

			err := app.Run(context.Background(), args)

			// If Run returns an error (like missing a required flag), that's our actualErr
			if err != nil {
				actualErr = err
			}

			if testCase.expectedErr != "" {
				if actualErr == nil {
					assert.FailNow(t, "Expected error %v but got nothing.", testCase.expectedErr)
				}
				assert.True(t, strings.Contains(actualErr.Error(), testCase.expectedErr), "Expected error %v but got error %v", testCase.expectedErr, actualErr)
			} else {
				assert.Nil(t, actualErr, "Unexpected error: %v", actualErr)
				if testCase.expectedOptions.Ports != nil && len(testCase.expectedOptions.Ports) == 0 {
					testCase.expectedOptions.Ports = make([]int, 0)
				}
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
	assert.Equal(t, expected.HttpReadTimeout, actual.HttpReadTimeout, msgAndArgs...)
	assert.Equal(t, expected.HttpWriteTimeout, actual.HttpWriteTimeout, msgAndArgs...)
	assert.Equal(t, expected.HttpIdleTimeout, actual.HttpIdleTimeout, msgAndArgs...)
	assert.Equal(t, expected.TcpDialTimeout, actual.TcpDialTimeout, msgAndArgs...)
	assert.Equal(t, expected.Scripts, actual.Scripts, msgAndArgs...)
	assert.Equal(t, expected.Listener, actual.Listener, msgAndArgs...)
	assert.Equal(t, expected.Ports, actual.Ports, msgAndArgs...)
}

func createOptionsForTest(t *testing.T, scriptTimeout int, scripts []string, listener string, ports []int) *options.Options {
	opts := &options.Options{}
	opts.ScriptTimeout = scriptTimeout
	opts.HttpReadTimeout = 5
	opts.HttpWriteTimeout = 0
	opts.HttpIdleTimeout = 15
	opts.TcpDialTimeout = 5

	parsedScripts, err := options.ParseScripts(scripts)
	assert.NoError(t, err)
	opts.Scripts = parsedScripts

	opts.Listener = listener
	opts.Ports = ports
	return opts
}
