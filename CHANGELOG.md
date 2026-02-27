# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Changed
- **Migrated CLI Framework to `urfave/cli/v3`**
  - **Replaced `cli.App` with `cli.Command`:** In version 3 of the `urfave/cli` package, the concept of a separate `App` struct has been removed in favor of using `cli.Command` as the root of the application. The `CreateCli` function and the underlying structure were updated to return and configure a `*cli.Command`.
  - **Context Handling:** Replaced the legacy `*cli.Context` with standard `context.Context` and `*cli.Command` references across the commands. Actions are now defined with the signature `func(ctx context.Context, cmd *cli.Command) error`.
  - **Flag Types Updated to Pointers:** The definitions for flags (e.g. `cli.IntSliceFlag`, `cli.StringSliceFlag`, `cli.IntFlag`, `cli.BoolFlag`, `cli.StringFlag`) were converted from value types to pointer types (`&cli.IntSliceFlag{}`, etc.) as required by the v3 API.
  - **Flag Parsing Updates:** Updated option parsing logic to correctly interact with v3 flag return types. Specifically, `cmd.IntSlice` now returns an `[]int64` rather than `[]int`, so the code was updated to convert these back to `[]int` for compatibility with the existing `options.Options` struct.
  - **Test Infrastructure Revamp:** The previous testing strategy involved manually constructing an arbitrary `flag.FlagSet` and feeding it to `cli.NewContext`. This API was completely removed in v3. Tests in `commands/flags_test.go` were refactored to execute the actual application via `app.Run(context.Background(), args)` using a mock action to intercept the parsed configuration and validate the options directly.
- **Removed `go-commons/entrypoint` Dependency:**
  - Removed the dependency on `github.com/gruntwork-io/go-commons/entrypoint`. The entrypoint in `main.go` was simplified to run the `urfave/cli` app directly via `app.Run(os.Args)` and manually exit via `os.Exit(1)` if an error occurs.
- **Removed Author Field from CLI:**
  - Removed the unsupported `app.Author` field configuration in `commands/cli.go` since it does not exist in `cli.Command` in `urfave/cli/v3`.

### Added
- **Detailed JSON Status Reporting:**
  - Added a new optional `--detailed-status` flag. When enabled, health check failures respond with a descriptive JSON payload rather than a plain string. The payload indicates the `elapsed_time` of the probes, a unified `status` text, and a comprehensive array of `errors` explaining exactly which TCP connections or script targets failed and why. This greatly improves debuggability when integrating with intelligent load balancers or API gateways.

### Performance
- **Early Short-Circuiting for Failing Probes:**
  - Standardized health check probes (TCP connections and scripts) to use a shared cancellation context. The very first failing probe will immediately cancel the execution of all other running parallel probes, returning a `504` error to the load balancer instantly rather than waiting for other slower timeouts to expire. This dramatically reduces worst-case latency during outages.

### Fixed
- **Improved HTTP Server Resilience:**
  - The health checker HTTP listener (`http.ListenAndServe`) previously used default Go configurations which lacked structural read/write timeouts. This can expose servers to resource exhaustion attacks from slow or hanging clients (e.g. slowloris). The HTTP server logic was refactored to use a custom `http.Server` with explicit `ReadTimeout`, `IdleTimeout`, and dynamically computed `WriteTimeout` (based on configured script timeouts + buffer) for superior performance and resilience as a robust probe target.
- **Configurable Network Timeouts:**
  - To provide maximum control over the load balancer connection lifetimes, several hardcoded timeout values were elevated into fully configurable command-line flags. Users can now pass:
    - `--http-read-timeout` (Default 5s)
    - `--http-write-timeout` (Dynamically defaults to `script-timeout + 5s`)
    - `--http-idle-timeout` (Default 15s)
    - `--tcp-dial-timeout` (Default 5s)
- **Resolved Data Race in Parallel Probes:**
  - The health checker executes configured TCP port and script checks concurrently using goroutines for efficiency. However, a data race existed where multiple parallel probes could attempt to write to the shared `allChecksOk` boolean simultaneously if they failed. This was fixed by migrating `allChecksOk` to a thread-safe `atomic.Bool` structure, ensuring safe and predictable cross-platform concurrent execution.
- **Test Suite Stabilizations:**
  - Fixed Windows-specific flaky tests where the `TestSingleflight` block attempted to use `/bin/sleep`. It now dynamically falls back to a PowerShell sleep command if running on Windows.
  - Mitigated a testing data race condition in `flags_test.go` due to parallel tests mutating global package variables by running the tests sequentially instead of concurrently.
  - Fixed a resource leak in the test suite (`server_test.go`) where concurrent singleflight tests failed to close the `http.Get` response body, leaving lingering sockets.
- **Improved Script Error Observability:**
  - Upgraded script execution logging to use `cmd.CombinedOutput()` instead of `cmd.Output()`. This ensures that if a health check script fails and writes to `stderr` instead of `stdout`, the error is properly captured and surfaced in the `--detailed-status` JSON payload, making debugging significantly easier.
- **Resolved `logging.GetLogger` Argument Arity:**
  - The `logging.GetLogger` method from `github.com/gruntwork-io/go-commons` was updated upstream to require a second parameter representing the application version. The calls in `commands/flags.go` and `server/server_test.go` were updated to pass a default `"v0.0.0"` version string, resolving a compilation error reported by `go vet`.
- **Corrected `logrus` Entry vs Logger usage:**
  - `logging.GetLogger` returns a `*logrus.Entry`. However, several parts of the application (like `options.Options` and direct property assignments) require the underlying `*logrus.Logger` instance. The code was updated to explicitly reference `.Logger` when setting the log level (`logger.Logger.SetLevel`), configuring standard output (`logger.Logger.Out`), and assigning to the options struct (`opts.Logger = logger.Logger`). This resolved several `undefined field or method` issues across the codebase.
