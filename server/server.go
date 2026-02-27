package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
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
		go func(portStr string) {
			defer waitGroup.Done()

			err := attemptTcpConnection(masterCtx, portStr, opts)
			if err != nil {
				// Don't report context cancelation as an explicit "failure" to avoid noise
				if errors.Is(err, context.Canceled) {
					return
				}

				logger.Warnf("TCP connection to %s FAILED: %s", portStr, err)
				errorMu.Lock()
				errorMessages = append(errorMessages, fmt.Sprintf("TCP connection to %s failed: %s", portStr, err.Error()))
				errorMu.Unlock()

				masterCancel()
			} else {
				logger.Infof("TCP connection to %s successful", portStr)
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

	for _, httpCheck := range opts.HttpChecks {
		waitGroup.Add(1)
		go func(httpCheck options.HttpCheck) {
			defer waitGroup.Done()

			err := attemptHttpConnection(masterCtx, httpCheck, opts)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}

				logger.Warnf("HTTP check to %s FAILED: %s", httpCheck.Url, err)
				errorMu.Lock()
				errorMessages = append(errorMessages, fmt.Sprintf("HTTP check to %s failed: %s", httpCheck.Url, err.Error()))
				errorMu.Unlock()

				masterCancel()
			} else {
				logger.Infof("HTTP check to %s successful", httpCheck.Url)
			}
		}(httpCheck)
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
		logger.Infof("All health checks passed. Returning HTTP 200 response.")
	} else {
		logger.Infof("At least one health check failed. Returning HTTP 504 response.")
	}

	return &httpResponse{StatusCode: statusCode, Body: body, ContentType: contentType}
}

// Attempt to open a TCP connection to the given address (can be port only or host:port)
func attemptTcpConnection(ctx context.Context, portStr string, opts *options.Options) error {
	logger := opts.Logger
	logger.Infof("Attempting to connect to %s via TCP...", portStr)

	defaultTimeout := time.Duration(opts.TcpDialTimeout) * time.Second
	if defaultTimeout == 0 {
		defaultTimeout = time.Second * 5
	}

	dialer := net.Dialer{Timeout: defaultTimeout}

	// If only a port is provided, default to 0.0.0.0
	address := portStr
	if !strings.Contains(address, ":") {
		address = fmt.Sprintf("0.0.0.0:%s", portStr)
	}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}

	defer func() {
		_ = conn.Close()
	}()

	return nil
}

// Attempt to perform an HTTP(S) GET request and optionally verify the payload
func attemptHttpConnection(ctx context.Context, httpCheck options.HttpCheck, opts *options.Options) error {
	logger := opts.Logger
	logger.Infof("Attempting to perform HTTP check to %s...", httpCheck.Url)

	defaultTimeout := time.Duration(opts.HttpDialTimeout) * time.Second
	if defaultTimeout == 0 {
		defaultTimeout = time.Second * 5
	}

	// Create a new client to avoid sharing state or keeping keep-alives open unnecessarily
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if opts.AllowInsecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402
	}

	client := &http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpCheck.Url, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	/* #nosec G107 G704 */
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Discard the body if we don't need it, but always read to allow connection reuse/clean closure
	var bodyBytes []byte
	if httpCheck.VerifyPayload != "" {
		bodyBytes, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read HTTP response body: %w", err)
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP check returned non-2xx status code: %d", resp.StatusCode)
	}

	if httpCheck.VerifyPayload != "" {
		matched, err := regexp.Match(httpCheck.VerifyPayload, bodyBytes)
		if err != nil {
			return fmt.Errorf("invalid regular expression '%s': %w", httpCheck.VerifyPayload, err)
		}
		if !matched {
			return fmt.Errorf("HTTP response body did not match verify-payload regex '%s'", httpCheck.VerifyPayload)
		}
	}

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
