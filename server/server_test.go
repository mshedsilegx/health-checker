package server

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gruntwork-io/health-checker/options"
	"github.com/gruntwork-io/health-checker/test"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestRunChecks(t *testing.T) {
	// Will *not* run parallel because we're opening random tcp ports
	// and want to avoid port clashes
	testCases := []struct {
		name           string
		numTcpPorts    int
		numHttpPorts   int
		failTcpPort    bool
		failHttpPort   bool
		httpUrl        string
		httpMatch      string
		scripts        []string
		scriptTimeout  int
		expectedStatus int
	}{
		{
			"tcp port check",
			1,
			0,
			false,
			false,
			"",
			"",
			[]string{},
			5,
			200,
		},
		{
			"multiport tcp check",
			3,
			0,
			false,
			false,
			"",
			"",
			[]string{},
			5,
			200,
		},
		{
			"multiport tcp check one fails",
			3,
			0,
			true,
			false,
			"",
			"",
			[]string{},
			5,
			504,
		},
		{
			"http port check",
			0,
			1,
			false,
			false,
			"",
			"",
			[]string{},
			5,
			200,
		},
		{
			"script ok",
			0,
			0,
			false,
			false,
			"",
			"",
			[]string{"echo 'hello'"},
			5,
			200,
		},
		{
			"script fail",
			0,
			0,
			false,
			false,
			"",
			"",
			[]string{"lskdf"},
			5,
			504,
		},
		{
			"multi script ok",
			0,
			0,
			false,
			false,
			"",
			"",
			[]string{"echo 'hello1'", "echo 'hello2'"},
			5,
			200,
		},
		{
			"multi script one fail",
			0,
			0,
			false,
			false,
			"",
			"",
			[]string{"echo 'hello1'", "lskdf"},
			5,
			504,
		},
		{
			"script and tcp port",
			1,
			0,
			false,
			false,
			"",
			"",
			[]string{"echo 'hello1'"},
			5,
			200,
		},
		{
			"http port check fail",
			0,
			1,
			false,
			true,
			"",
			"",
			[]string{},
			5,
			504,
		},
		{
			"http url check",
			0,
			0,
			false,
			false,
			"", // Will be replaced with mock server URL
			"",
			[]string{},
			5,
			200,
		},
		{
			"http url check fail",
			0,
			0,
			false,
			false,
			"http://localhost:12346", // A port that is guaranteed to be free
			"",
			[]string{},
			5,
			504,
		},
		{
			"http match check",
			0,
			0,
			false,
			false,
			"", // Will be replaced with mock server URL
			"OK",
			[]string{},
			5,
			200,
		},
		{
			"http match check fail",
			0,
			0,
			false,
			false,
			"", // Will be replaced with mock server URL
			"FAIL",
			[]string{},
			5,
			504,
		},
	}

	// Create a mock HTTP server that returns a 200 OK with the body "OK"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer ts.Close()

	for i, testCase := range testCases {
		if testCase.name == "http url check" || testCase.name == "http match check" || testCase.name == "http match check fail" {
			testCases[i].httpUrl = ts.URL
		}
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tcpPorts, err := test.GetFreePorts(1 + testCase.numTcpPorts)
			if err != nil {
				assert.FailNow(t, "Failed to get free tcp ports: %v", err.Error())
			}

			httpPorts, err := test.GetFreePorts(testCase.numHttpPorts)
			if err != nil {
				assert.FailNow(t, "Failed to get free http ports: %v", err.Error())
			}

			listenerString := test.ListenerString(test.DEFAULT_LISTENER_ADDRESS, tcpPorts[0])

			checkTcpPorts := []int{}
			listenTcpPorts := []int{}

			// If we're monitoring tcp ports, prepare them
			if testCase.numTcpPorts > 0 {
				checkTcpPorts = tcpPorts[1:]
				listenTcpPorts = make([]int, len(checkTcpPorts))
				copy(listenTcpPorts, checkTcpPorts)

				// If we want to fail one check, remove the first port from the listen ports
				// So the health-check cannot connect
				if testCase.failTcpPort {
					listenTcpPorts = listenTcpPorts[1:]
				}
			}

			checkHttpPorts := []int{}

			if testCase.numHttpPorts > 0 {
				listenHttpPorts := make([]int, testCase.numHttpPorts)
				copy(listenHttpPorts, httpPorts)

				if testCase.failHttpPort {
					checkHttpPorts = httpPorts
				} else {
					for range listenHttpPorts {
						ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							w.WriteHeader(http.StatusOK)
							w.Write([]byte("OK"))
						}))
						defer ts.Close()

						parsedUrl, err := url.Parse(ts.URL)
						if err != nil {
							assert.FailNow(t, "Failed to parse http test server url: %v", err.Error())
						}
						p, err := strconv.Atoi(parsedUrl.Port())
						if err != nil {
							assert.FailNow(t, "Failed to parse http test server port: %v", err.Error())
						}
						checkHttpPorts = append(checkHttpPorts, p)
					}
				}
			}

			tcpListeners := []net.Listener{}
			for _, port := range listenTcpPorts {
				t.Logf("Creating listener for port %d", port)
				l, err := net.Listen("tcp", test.ListenerString(test.DEFAULT_LISTENER_ADDRESS, port))
				if err != nil {
					t.Logf("Error creating listener for port %d: %s", port, err.Error())
					assert.FailNow(t, "Failed to start listening: %s", err.Error())
				}
				tcpListeners = append(tcpListeners, l)
				go handleRequests(t, l, nil)
			}
			defer closeListeners(t, tcpListeners)

			opts := createOptionsForTest(t, testCase.scriptTimeout, testCase.scripts, listenerString, checkTcpPorts)
			opts.HttpPorts = checkHttpPorts
			opts.HttpUrl = testCase.httpUrl
			opts.HttpMatch = testCase.httpMatch

			// Run the checks and verify the status code
			response := runChecks(opts)
			assert.True(t, testCase.expectedStatus == response.StatusCode, "Got expected status code")
		})
	}
}

func TestJsonOutput(t *testing.T) {
	t.Parallel()

	// This test case should fail the TCP check but pass the script check
	opts := createOptionsForTest(t, 5, []string{"echo 'hello'"}, "0.0.0.0:12345", []int{12346})
	opts.ReturnJson = true

	response := runChecks(opts)
	assert.Equal(t, http.StatusOK, response.StatusCode)

	var result JsonResult
	err := json.Unmarshal([]byte(response.Body), &result)
	if assert.NoError(t, err) {
		assert.Equal(t, "FAIL", result.Status)
		assert.ElementsMatch(t, []int{12346}, result.Message.Content.FailedTcpPorts)
		assert.ElementsMatch(t, []int{}, result.Message.Content.SuccessTcpPorts)
		assert.ElementsMatch(t, []string{"'hello'\n"}, result.Message.Content.ScriptResults)
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

			// Fire the request off to /bin/sleep to ensure it takes a while
			opts := createOptionsForTest(t, 10, []string{"/bin/sleep 1"}, test.DEFAULT_LISTENER_ADDRESS, []int{port})
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

func createOptionsForTest(t *testing.T, scriptTimeout int, scripts []string, listener string, ports []int) *options.Options {
	logger := logrus.New()
	logger.Out = os.Stdout
	logger.Level = logrus.InfoLevel

	opts := &options.Options{}
	opts.Logger = logger
	opts.ScriptTimeout = scriptTimeout
	opts.Scripts = options.ParseScripts(scripts)
	opts.Listener = listener
	opts.Ports = ports
	return opts
}
