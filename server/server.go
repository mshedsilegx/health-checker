package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"time"

	commons_errors "github.com/gruntwork-io/go-commons/errors"
	"github.com/gruntwork-io/health-checker/options"
	"golang.org/x/sync/singleflight"
)

type httpResponse struct {
	StatusCode  int
	Body        string
	ContentType string
}

// DetailedResponse represents a detailed health check response.
// It includes the status of the health check, the elapsed time, and any errors that occurred.
type DetailedResponse struct {
	Status      string   `json:"status"`
	ElapsedTime string   `json:"elapsed_time"`
	Errors      []string `json:"errors,omitempty"`
}

// StartHttpServer starts the health-check HTTP server.
// It leverages strict connection timeouts (Read, Write, Idle) to prevent resource exhaustion attacks
// such as Slowloris, keeping the health checker resilient under degraded network conditions.
func StartHttpServer(opts *options.Options) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", httpHandler(opts))

	// Resolve dynamic default for WriteTimeout if not explicitly provided
	// Must allow the scripts to run, plus buffer for generating response
	writeTimeout := time.Duration(opts.HttpWriteTimeout) * time.Second
	if writeTimeout == 0 {
		writeTimeout = time.Duration(opts.ScriptTimeout+5) * time.Second
	}

	readTimeout := time.Duration(opts.HttpReadTimeout) * time.Second
	if readTimeout == 0 {
		readTimeout = 5 * time.Second
	}

	idleTimeout := time.Duration(opts.HttpIdleTimeout) * time.Second
	if idleTimeout == 0 {
		idleTimeout = 15 * time.Second
	}

	srv := &http.Server{
		Addr:         opts.Listener,
		Handler:      mux,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}

	err := srv.ListenAndServe()
	if err != nil {
		return err
	}

	return nil
}

// httpHandler processes inbound HTTP requests to the health-check endpoint.
// It acts as the routing logic between Singleflight execution (collapsed concurrent requests)
// and standard execution.
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

// runChecks performs all configured health checks (TCP ports and scripts) in parallel using goroutines.
// It leverages early short-circuiting: a master cancellation context ensures that if any single probe fails,
// all other actively running probes are immediately aborted to return a swift 504 error to the load balancer
// without waiting for maximum timeouts to be reached.
func runChecks(opts *options.Options) *httpResponse {
	logger := opts.Logger

	startTime := time.Now()

	var errorMessages []string
	var errorMu sync.Mutex

	var waitGroup = sync.WaitGroup{}

	// Create a master context that can be canceled
	// If detailed status is not requested, the first error will trigger cancellation
	masterCtx, masterCancel := context.WithCancel(context.Background())
	defer masterCancel()

	for _, port := range opts.Ports {
		waitGroup.Add(1)
		go func(port int) {
			defer waitGroup.Done()

			err := attemptTcpConnection(masterCtx, port, opts)
			if err != nil {
				// Don't report context cancelation as an explicit "failure" to avoid noise
				if errors.Is(err, context.Canceled) {
					return
				}

				logger.Warnf("TCP connection to port %d FAILED: %s", port, err)
				errorMu.Lock()
				errorMessages = append(errorMessages, fmt.Sprintf("TCP connection to port %d failed: %s", port, err.Error()))
				errorMu.Unlock()

				masterCancel()
			} else {
				logger.Infof("TCP connection to port %d successful", port)
			}
		}(port)
	}

	for _, script := range opts.Scripts {
		waitGroup.Add(1)
		go func(script options.Script) {
			defer waitGroup.Done()

			logger.Infof("Executing '%v' with a timeout of %v seconds...", script, opts.ScriptTimeout)

			timeout := time.Second * time.Duration(opts.ScriptTimeout)

			// Use the masterCtx as the parent so that if it is canceled, the script terminates immediately
			ctx, cancel := context.WithTimeout(masterCtx, timeout)
			defer cancel()

			/* #nosec G204 */
			cmd := exec.CommandContext(ctx, script.Name, script.Args...)
			output, err := cmd.CombinedOutput()

			if err != nil {
				// Avoid logging context cancellation errors from short-circuiting
				if masterCtx.Err() != nil {
					return
				}

				logger.Warnf("Script %v FAILED: %s", script.Name, err)
				logger.Warnf("Command output: %s", output)
				errorMu.Lock()
				errorMessages = append(errorMessages, fmt.Sprintf("Script %v failed: %s (Output: %s)", script.Name, err.Error(), string(output)))
				errorMu.Unlock()

				masterCancel()
			} else {
				logger.Infof("Script %v successful", script)
			}
		}(script)
	}

	waitGroup.Wait()

	elapsedTime := time.Since(startTime).String()

	statusCode := http.StatusOK
	statusText := "OK"
	body := "OK"
	contentType := "text/plain"

	if len(errorMessages) > 0 {
		statusCode = http.StatusGatewayTimeout
		statusText = "At least one health check failed"
		body = statusText
	}

	if opts.DetailedStatus {
		contentType = "application/json"
		detailedResp := DetailedResponse{
			Status:      statusText,
			ElapsedTime: elapsedTime,
			Errors:      errorMessages,
		}
		jsonBytes, err := json.Marshal(detailedResp)
		if err == nil {
			body = string(jsonBytes)
		} else {
			logger.Warnf("Failed to marshal detailed status JSON: %v", err)
			body = `{"status":"error_marshalling_json"}`
		}
	}

	if statusCode == http.StatusOK {
		logger.Infof("All health checks passed. Returning HTTP 200 response.\n")
	} else {
		logger.Infof("At least one health check failed. Returning HTTP 504 response.\n")
	}

	return &httpResponse{StatusCode: statusCode, Body: body, ContentType: contentType}
}

// Attempt to open a TCP connection to the given port
func attemptTcpConnection(ctx context.Context, port int, opts *options.Options) error {
	logger := opts.Logger
	logger.Infof("Attempting to connect to port %d via TCP...", port)

	defaultTimeout := time.Duration(opts.TcpDialTimeout) * time.Second
	if defaultTimeout == 0 {
		defaultTimeout = time.Second * 5
	}

	dialer := net.Dialer{Timeout: defaultTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return err
	}

	defer func() {
		_ = conn.Close()
	}()

	return nil
}

func writeHttpResponse(w http.ResponseWriter, resp *httpResponse) error {
	if resp.ContentType != "" {
		w.Header().Set("Content-Type", resp.ContentType)
	} else {
		w.Header().Set("Content-Type", "text/plain")
	}
	w.WriteHeader(resp.StatusCode)
	_, err := w.Write([]byte(resp.Body))
	if err != nil {
		return commons_errors.WithStackTrace(err)
	}
	return nil
}
