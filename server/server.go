package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/gruntwork-io/go-commons/errors"
	"github.com/gruntwork-io/health-checker/options"
	"golang.org/x/sync/singleflight"
)

type httpResponse struct {
	StatusCode int
	Body       string
}

func StartHttpServer(opts *options.Options) error {
	http.HandleFunc("/", httpHandler(opts))

	err := http.ListenAndServe(opts.Listener, nil)
	if err != nil {
		return err
	}

	return nil
}

func httpHandler(opts *options.Options) http.HandlerFunc {
	var group singleflight.Group

	return func(w http.ResponseWriter, r *http.Request) {
		var resp *httpResponse
		logger := opts.Logger

		// In Singleflight mode only one runChecks pass will be performed
		// at any given time, with the result being shared across concurrent
		// inbound requests
		if opts.Singleflight {
			logger.Infof("Received inbound request. Performing singleflight health checks...")

			result, _, shared := group.Do("check", func() (interface{}, error) {
				logger.Infof("Beginning health checks...")
				return runChecks(opts), nil
			})

			if shared {
				logger.Infof("Singleflight health check response was shared between multiple requests.")
			}

			resp = result.(*httpResponse)
		} else {
			logger.Infof("Received inbound request. Beginning health checks...")
			resp = runChecks(opts)
		}

		err := writeHttpResponse(w, resp)
		if err != nil {
			opts.Logger.Error("Failed to send HTTP response. Exiting.")
			panic(err)
		}
	}
}

// Check that we can open a TPC connection to all the ports in opts.Ports
func runChecks(opts *options.Options) *httpResponse {
	logger := opts.Logger

	// We use a mutex to protect access to allChecksOk so that it can be safely updated by multiple goroutines
	allChecksOk := true
	var mutex = &sync.Mutex{}

	var waitGroup = sync.WaitGroup{}

	runTcpChecks(opts, &waitGroup, &allChecksOk, mutex)
	runScriptChecks(opts, &waitGroup, &allChecksOk, mutex)

	waitGroup.Wait()

	if allChecksOk {
		logger.Infof("All health checks passed. Returning HTTP 200 response.\n")
		return &httpResponse{StatusCode: http.StatusOK, Body: "OK"}
	} else {
		logger.Infof("At least one health check failed. Returning HTTP 504 response.\n")
		return &httpResponse{StatusCode: http.StatusGatewayTimeout, Body: "At least one health check failed"}
	}
}

// Concurrently run all the TCP health checks
func runTcpChecks(opts *options.Options, waitGroup *sync.WaitGroup, allChecksOk *bool, mutex *sync.Mutex) {
	logger := opts.Logger

	for _, port := range opts.Ports {
		waitGroup.Add(1)
		go func(port int) {
			defer waitGroup.Done()

			err := attemptTcpConnection(port, opts)
			if err != nil {
				logger.Warnf("TCP connection to port %d FAILED: %s", port, err)
				mutex.Lock()
				*allChecksOk = false
				mutex.Unlock()
			} else {
				logger.Infof("TCP connection to port %d successful", port)
			}
		}(port)
	}
}

// Concurrently run all the script health checks
func runScriptChecks(opts *options.Options, waitGroup *sync.WaitGroup, allChecksOk *bool, mutex *sync.Mutex) {
	logger := opts.Logger

	for _, script := range opts.Scripts {
		waitGroup.Add(1)
		go func(script options.Script) {

			defer waitGroup.Done()

			logger.Infof("Executing '%v' with a timeout of %v seconds...", script, opts.ScriptTimeout)

			timeout := time.Second * time.Duration(opts.ScriptTimeout)

			ctx, cancel := context.WithTimeout(context.Background(), timeout)

			defer cancel()

			cmd := exec.CommandContext(ctx, script.Name, script.Args...)

			output, err := cmd.Output()

			if err != nil {
				logger.Warnf("Script %v FAILED: %s", script.Name, err)
				logger.Warnf("Command output: %s", output)
				mutex.Lock()
				*allChecksOk = false
				mutex.Unlock()
			} else {
				logger.Infof("Script %v successful", script)
			}
		}(script)
	}
}

// Attempt to open a TCP connection to the given port
func attemptTcpConnection(port int, opts *options.Options) error {
	logger := opts.Logger
	logger.Infof("Attempting to connect to port %d via TCP with a timeout of %v seconds...", port, opts.TcpTimeout)

	timeout := time.Second * time.Duration(opts.TcpTimeout)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("0.0.0.0:%d", port), timeout)
	if err != nil {
		return err
	}

	defer func() {
		_ = conn.Close()
	}()

	return nil
}

func writeHttpResponse(w http.ResponseWriter, resp *httpResponse) error {
	w.WriteHeader(resp.StatusCode)
	_, err := w.Write([]byte(resp.Body))
	if err != nil {
		return errors.WithStackTrace(err)
	}

	return nil
}
