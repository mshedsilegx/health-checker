package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/gruntwork-io/go-commons/errors"
	"github.com/gruntwork-io/health-checker/options"
	"github.com/sirupsen/logrus"
	"github.com/wasilibs/go-re2"
	"golang.org/x/sync/singleflight"
)

type httpResponse struct {
	StatusCode int
	Body       string
}

type JsonResult struct {
	Status  string        `json:"status"`
	Message MessageResult `json:"message"`
}

type MessageResult struct {
	Code    int           `json:"code"`
	Content ContentResult `json:"content"`
}

type ContentResult struct {
	FailedTcpPorts   []int    `json:"failed_tcp_ports"`
	SuccessTcpPorts  []int    `json:"success_tcp_ports"`
	FailedHttpPorts  []int    `json:"failed_http_ports"`
	SuccessHttpPorts []int    `json:"success_http_ports"`
	FailedHttpUrls   []string `json:"failed_http_urls"`
	SuccessHttpUrls  []string `json:"success_http_urls"`
	ScriptResults    []string `json:"script_results"`
}

type checkChannels struct {
	failedTcpPorts   chan int
	successTcpPorts  chan int
	failedHttpPorts  chan int
	successHttpPorts chan int
	failedHttpUrls   chan string
	successHttpUrls  chan string
	scriptResults    chan string
}

func StartHttpServer(opts *options.Options) error {
	http.HandleFunc("/", httpHandler(opts))

	err := http.ListenAndServe(opts.Listener, nil)
	if err != nil {
		return errors.WithStackTrace(err)
	}

	return nil
}

// Attempt to open a HTTP connection to the given url
func attemptHttpConnection(url string, opts *options.Options) error {
	logger := opts.Logger
	logger.Infof("Attempting to connect to %s via HTTP...", url)

	timeout := time.Second * time.Duration(opts.HttpTimeout)
	client := http.Client{
		Timeout: timeout,
	}

	resp, err := client.Get(url)
	if err != nil {
		return errors.WithStackTrace(err)
	}

	defer func() {
		err := resp.Body.Close()
		if err != nil {
			opts.Logger.Warnf("error closing response body: %s", err)
		}
	}()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		return errors.WithStackTrace(fmt.Errorf("expected status code 200 or 302, but got %d", resp.StatusCode))
	}

	if opts.HttpMatch != "" {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return errors.WithStackTrace(err)
		}

		match, err := re2.Match(opts.HttpMatch, body)
		if err != nil {
			return errors.WithStackTrace(err)
		}

		if !match {
			return errors.WithStackTrace(fmt.Errorf("could not find string %s in response body", opts.HttpMatch))
		}
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

	channels := newCheckChannels(opts)

	runTcpChecks(opts, &waitGroup, &allChecksOk, mutex, channels.failedTcpPorts, channels.successTcpPorts)
	runHttpChecks(opts, &waitGroup, &allChecksOk, mutex, channels.failedHttpPorts, channels.successHttpPorts)
	runHttpUrlChecks(opts, &waitGroup, &allChecksOk, mutex, channels.failedHttpUrls, channels.successHttpUrls)
	runScriptChecks(opts, &waitGroup, &allChecksOk, mutex, channels.scriptResults)

	waitGroup.Wait()

	closeChannels(channels)

	if opts.ReturnJson {
		return &httpResponse{StatusCode: http.StatusOK, Body: toJson(allChecksOk, channels.failedTcpPorts, channels.successTcpPorts, channels.failedHttpPorts, channels.successHttpPorts, channels.failedHttpUrls, channels.successHttpUrls, channels.scriptResults)}
	}

	if allChecksOk {
		logger.Infof("All health checks passed. Returning HTTP 200 response.\n")
		return &httpResponse{StatusCode: http.StatusOK, Body: "OK"}
	} else {
		logger.Infof("At least one health check failed. Returning HTTP 504 response.\n")
		return &httpResponse{StatusCode: http.StatusGatewayTimeout, Body: "At least one health check failed"}
	}
}

func newCheckChannels(opts *options.Options) *checkChannels {
	return &checkChannels{
		failedTcpPorts:   make(chan int, len(opts.Ports)),
		successTcpPorts:  make(chan int, len(opts.Ports)),
		failedHttpPorts:  make(chan int, len(opts.HttpPorts)),
		successHttpPorts: make(chan int, len(opts.HttpPorts)),
		failedHttpUrls:   make(chan string, 1),
		successHttpUrls:  make(chan string, 1),
		scriptResults:    make(chan string, len(opts.Scripts)),
	}
}

func closeChannels(channels *checkChannels) {
	close(channels.failedTcpPorts)
	close(channels.successTcpPorts)
	close(channels.failedHttpPorts)
	close(channels.successHttpPorts)
	close(channels.failedHttpUrls)
	close(channels.successHttpUrls)
	close(channels.scriptResults)
}

// Concurrently run all the TCP health checks
func runTcpChecks(opts *options.Options, waitGroup *sync.WaitGroup, allChecksOk *bool, mutex *sync.Mutex, failedTcpPorts chan int, successTcpPorts chan int) {
	logger := opts.Logger

	for _, port := range opts.Ports {
		waitGroup.Add(1)
		go func(port int) {
			defer waitGroup.Done()

			err := attemptTcpConnection(port, opts)
			if err != nil {
				logger.WithFields(logrus.Fields{
					"port":  port,
					"error": err,
				}).Warn("TCP connection to port failed")
				mutex.Lock()
				*allChecksOk = false
				mutex.Unlock()
				failedTcpPorts <- port
			} else {
				logger.WithField("port", port).Info("TCP connection to port successful")
				successTcpPorts <- port
			}
		}(port)
	}
}

// Concurrently run all the script health checks
func runHttpChecks(opts *options.Options, waitGroup *sync.WaitGroup, allChecksOk *bool, mutex *sync.Mutex, failedHttpPorts chan int, successHttpPorts chan int) {
	logger := opts.Logger

	for _, port := range opts.HttpPorts {
		waitGroup.Add(1)
		go func(port int) {
			defer waitGroup.Done()

			url := fmt.Sprintf("http://127.0.0.1:%d", port)
			err := attemptHttpConnection(url, opts)
			if err != nil {
				logger.WithFields(logrus.Fields{
					"port":  port,
					"error": err,
				}).Warn("HTTP connection to port failed")
				mutex.Lock()
				*allChecksOk = false
				mutex.Unlock()
				failedHttpPorts <- port
			} else {
				logger.WithField("port", port).Info("HTTP connection to port successful")
				successHttpPorts <- port
			}
		}(port)
	}
}

// Concurrently run all the script health checks
func runScriptChecks(opts *options.Options, waitGroup *sync.WaitGroup, allChecksOk *bool, mutex *sync.Mutex, scriptResults chan string) {
	logger := opts.Logger

	for _, script := range opts.Scripts {
		waitGroup.Add(1)
		go func(script options.Script) {

			defer waitGroup.Done()

			logger.WithField("script", script).Info("Executing script")

			timeout := time.Second * time.Duration(opts.ScriptTimeout)

			ctx, cancel := context.WithTimeout(context.Background(), timeout)

			defer cancel()

			cmd := exec.CommandContext(ctx, script.Name, script.Args...)

			output, err := cmd.Output()

			if err != nil {
				logger.WithFields(logrus.Fields{
					"script": script,
					"error":  err,
					"output": string(output),
				}).Warn("Script failed")
				mutex.Lock()
				*allChecksOk = false
				mutex.Unlock()
				scriptResults <- string(output)
			} else {
				logger.WithField("script", script).Info("Script successful")
				scriptResults <- string(output)
			}
		}(script)
	}
}

func runHttpUrlChecks(opts *options.Options, waitGroup *sync.WaitGroup, allChecksOk *bool, mutex *sync.Mutex, failedHttpUrls chan string, successHttpUrls chan string) {
	logger := opts.Logger

	if opts.HttpUrl != "" {
		waitGroup.Add(1)
		go func(url string) {
			defer waitGroup.Done()

			err := attemptHttpConnection(url, opts)
			if err != nil {
				logger.WithFields(logrus.Fields{
					"url":   url,
					"error": err,
				}).Warn("HTTP connection to URL failed")
				mutex.Lock()
				*allChecksOk = false
				mutex.Unlock()
				failedHttpUrls <- url
			} else {
				logger.WithField("url", url).Info("HTTP connection to URL successful")
				successHttpUrls <- url
			}
		}(opts.HttpUrl)
	}
}

// Attempt to open a TCP connection to the given port
func attemptTcpConnection(port int, opts *options.Options) error {
	logger := opts.Logger
	logger.Infof("Attempting to connect to port %d via TCP with a timeout of %v seconds...", port, opts.TcpTimeout)

	timeout := time.Second * time.Duration(opts.TcpTimeout)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("0.0.0.0:%d", port), timeout)
	if err != nil {
		return errors.WithStackTrace(err)
	}

	defer func() {
		err := conn.Close()
		if err != nil {
			opts.Logger.Warnf("error closing TCP connection: %s", err)
		}
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

func toJson(allChecksOk bool, failedTcpPorts chan int, successTcpPorts chan int, failedHttpPorts chan int, successHttpPorts chan int, failedHttpUrls chan string, successHttpUrls chan string, scriptResults chan string) string {
	status := "OK"
	code := 200
	if !allChecksOk {
		status = "FAIL"
		code = 504
	}

	content := ContentResult{
		FailedTcpPorts:   []int{},
		SuccessTcpPorts:  []int{},
		FailedHttpPorts:  []int{},
		SuccessHttpPorts: []int{},
		FailedHttpUrls:   []string{},
		SuccessHttpUrls:  []string{},
		ScriptResults:    []string{},
	}

	for port := range failedTcpPorts {
		content.FailedTcpPorts = append(content.FailedTcpPorts, port)
	}
	for port := range successTcpPorts {
		content.SuccessTcpPorts = append(content.SuccessTcpPorts, port)
	}
	for port := range failedHttpPorts {
		content.FailedHttpPorts = append(content.FailedHttpPorts, port)
	}
	for port := range successHttpPorts {
		content.SuccessHttpPorts = append(content.SuccessHttpPorts, port)
	}
	for url := range failedHttpUrls {
		content.FailedHttpUrls = append(content.FailedHttpUrls, url)
	}
	for url := range successHttpUrls {
		content.SuccessHttpUrls = append(content.SuccessHttpUrls, url)
	}
	for result := range scriptResults {
		content.ScriptResults = append(content.ScriptResults, result)
	}

	jsonResult := JsonResult{
		Status: status,
		Message: MessageResult{
			Code:    code,
			Content: content,
		},
	}

	jsonBytes, err := json.Marshal(jsonResult)
	if err != nil {
		return "{\"status\": \"FAIL\", \"message\": {\"code\": 500, \"content\": \"Failed to marshal JSON\"}}"
	}

	return string(jsonBytes)
}
