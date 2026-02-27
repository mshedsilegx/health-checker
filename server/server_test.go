package server

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"runtime"

	"sync"
	"sync/atomic"
	"testing"

	"github.com/gruntwork-io/go-commons/logging"
	"github.com/gruntwork-io/health-checker/options"
	"github.com/gruntwork-io/health-checker/test"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
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

func TestStartHttpServerInvalidListener(t *testing.T) {
	opts := &options.Options{
		Listener: "256.256.256.256:9999999", // Invalid IP and port to force listen failure
		Logger:   logging.GetLogger("test", "v0.0.0").Logger,
	}

	err := StartHttpServer(opts)
	assert.Error(t, err, "Expected StartHttpServer to fail with invalid listener")
}

func TestParseChecksFromConfig(t *testing.T) {
	tmpDir := t.TempDir()
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	okScript := createDummyScript(t, tmpDir, "ok_script", "echo ok")
	failScript := createDummyScript(t, tmpDir, "fail_script", "exit 1")
	okScript2 := createDummyScript(t, tmpDir, "ok_script2", "echo ok2")

	// Will *not* run parallel because we're opening random tcp ports
	// and want to avoid port clashes
	testCases := []struct {
		name           string
		numports       int
		failport       bool
		scripts        []string
		httpChecks     []options.HttpCheck
		scriptTimeout  int
		expectedStatus int
	}{
		{
			"port check",
			1,
			false,
			[]string{},
			nil,
			5,
			200,
		},
		{
			"multiport check",
			3,
			false,
			[]string{},
			nil,
			5,
			200,
		},
		{
			"multiport check one fails",
			3,
			true,
			[]string{},
			nil,
			5,
			504,
		},
		{
			"script ok",
			0,
			false,
			[]string{okScript},
			nil,
			5,
			200,
		},
		{
			"script fail",
			0,
			false,
			[]string{failScript},
			nil,
			5,
			504,
		},
		{
			"multi script ok",
			0,
			false,
			[]string{okScript, okScript2},
			nil,
			5,
			200,
		},
		{
			"multi script one fail",
			0,
			false,
			[]string{okScript, failScript},
			nil,
			5,
			504,
		},
		{
			"script and port",
			1,
			false,
			[]string{okScript},
			nil,
			5,
			200,
		},
		{
			"http ok",
			0,
			false,
			[]string{},
			[]options.HttpCheck{{Url: "https://httpbin.org/status/200"}},
			5,
			200,
		},
		{
			"http fail 500",
			0,
			false,
			[]string{},
			[]options.HttpCheck{{Url: "https://httpbin.org/status/500"}},
			5,
			504,
		},
		{
			"http ok with matching payload",
			0,
			false,
			[]string{},
			[]options.HttpCheck{{Url: "https://httpbin.org/get", VerifyPayload: `"url": "https://httpbin.org/get"`}},
			5,
			200,
		},
		{
			"http fail with non-matching payload",
			0,
			false,
			[]string{},
			[]options.HttpCheck{{Url: "https://httpbin.org/get", VerifyPayload: "this-will-not-match-anything"}},
			5,
			504,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ports, err := test.GetFreePorts(1 + testCase.numports)

			if err != nil {
				assert.FailNow(t, "Failed to get free ports: %v", err.Error())
			}

			listenerString := test.ListenerString(test.DEFAULT_LISTENER_ADDRESS, ports[0])

			checkPorts := []string{}
			listenPorts := []int{}

			// If we're monitoring tcp ports, prepare them
			if testCase.numports > 0 {
				listenPorts = make([]int, len(ports[1:]))
				copy(listenPorts, ports[1:])

				for _, p := range listenPorts {
					checkPorts = append(checkPorts, fmt.Sprintf("%d", p))
				}

				// If we want to fail one check, remove the first port from the listen ports
				// So the health-check cannot connect
				if testCase.failport {
					listenPorts = listenPorts[1:]
				}
			}

			listeners := []net.Listener{}

			for _, port := range listenPorts {
				t.Logf("Creating listener for port %d", port)
				l, err := net.Listen("tcp", test.ListenerString(test.DEFAULT_LISTENER_ADDRESS, port))
				if err != nil {
					t.Logf("Error creating listener for port %d: %s", port, err.Error())
					assert.FailNow(t, "Failed to start listening: %s", err.Error())
				}

				listeners = append(listeners, l)

				// Separate goroutine for the tcp listeners
				go handleRequests(t, l, nil)
			}

			defer closeListeners(t, listeners)

			opts := createOptionsForTest(t, testCase.scriptTimeout, testCase.scripts, testCase.httpChecks, listenerString, checkPorts)

			// Run the checks and verify the status code
			response := runChecks(opts)
			assert.True(t, testCase.expectedStatus == response.StatusCode, "Got expected status code")
		})
	}
}

func TestSingleflight(t *testing.T) {

	testCases := []struct {
		name                 string
		singleflight         bool
		expectedRequestCount int32
	}{
		{
			"singleflight disabled",
			false,
			10,
		},
		{
			"singleflight enabled",
			true,
			1,
		},
	}

	tmpDir := t.TempDir()
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	sleepScript := createDummyScript(t, tmpDir, "sleep_script", "ping 127.0.0.1 -n 2")
	if runtime.GOOS != "windows" {
		sleepScript = createDummyScript(t, tmpDir, "sleep_script", "sleep 1")
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			requestCount := int32(0)

			ports, err := test.GetFreePorts(1)
			if err != nil {
				assert.FailNow(t, "Failed to get free ports: %v", err.Error())
			}

			port := ports[0]
			t.Logf("Creating listener for port %d", port)
			l, err := net.Listen("tcp", test.ListenerString(test.DEFAULT_LISTENER_ADDRESS, port))
			if err != nil {
				t.Logf("Error creating listener for port %d: %s", port, err.Error())
				assert.FailNow(t, "Failed to start listening: %s", err.Error())
			}

			// Accept incoming connections, and count how many we receive
			go handleRequests(t, l, &requestCount)
			defer func() {
				_ = l.Close()
			}()

			// Fire the request off to the dummy sleep script to ensure it takes a while
			opts := createOptionsForTest(t, 5, []string{sleepScript}, nil, test.ListenerString(test.DEFAULT_LISTENER_ADDRESS, port), []string{fmt.Sprintf("%d", port)})
			opts.Singleflight = testCase.singleflight

			handler := httpHandler(opts)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handler.ServeHTTP(w, r)
			}))
			defer ts.Close()

			// Fire off 10 concurrent requests. In Singleflight mode only one
			// underyling check should be performed.
			var wg sync.WaitGroup
			wg.Add(10)
			for i := 0; i < 10; i++ {
				go func() {
					resp, err := http.Get(ts.URL)
					if err != nil {
						assert.FailNow(t, "failed to perform HTTP request: %v", err)
					}

					_, _ = io.ReadAll(resp.Body)
					_ = resp.Body.Close() // Explicitly close to prevent resource leaks
					wg.Done()
				}()
			}
			wg.Wait()

			assert.Equal(t, testCase.expectedRequestCount, requestCount)
		})
	}

}

func closeListeners(t *testing.T, listeners []net.Listener) {
	for _, l := range listeners {
		err := l.Close()
		if err != nil {
			t.Fatal("Failed to close listener: ", err)
		}
	}
}

func handleRequests(t *testing.T, l net.Listener, counter *int32) {
	for {
		// Listen for an incoming connection.
		_, _ = l.Accept()
		// We don't log these when testing because we're forcibly closing the socket
		// from the outside. If you're debugging and wish to enable the logging,
		// uncomment the lines below
		//_, err := l.Accept()
		//if err != nil {
		//	t.Logf("Error accepting: %s", err.Error())
		//}

		if counter != nil {
			atomic.AddInt32(counter, 1)
		}
	}
}

func createOptionsForTest(t *testing.T, scriptTimeout int, scripts []string, httpChecks []options.HttpCheck, listener string, ports []string) *options.Options {
	logger := logging.GetLogger("health-checker", "v0.0.0")
	logger.Logger.Out = os.Stdout
	logger.Logger.Level = logrus.InfoLevel

	opts := &options.Options{}
	opts.Logger = logger.Logger
	opts.ScriptTimeout = scriptTimeout

	parsedScripts, err := options.ParseScripts(scripts)
	assert.NoError(t, err, "Failed to parse test scripts")
	opts.Scripts = parsedScripts
	opts.HttpChecks = httpChecks

	opts.Listener = listener
	opts.Ports = ports
	return opts
}
