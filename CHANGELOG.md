# Changelog

## 2025-11-11

### Security

- **Fixed Command Injection Vulnerability:** The `ParseScripts` function now uses the `go-shellwords` library to safely parse script strings, mitigating a command injection vulnerability.
- **Fixed SSRF Vulnerability:** Added validation to the `--http-url` flag to prevent Server-Side Request Forgery (SSRF) attacks.
- **Fixed ReDoS Vulnerability:** Replaced the `regexp.Match` function with `re2.Match` to prevent Regular Expression Denial of Service (ReDoS) attacks.

### Improvements

- **Consistent Error Handling:** Updated the codebase to use `errors.WithStackTrace` consistently for all errors that are returned from functions.

## 2025-11-07

### New Features

- **TCP Port Range Checking:** Added a new `--tcp-port-range` flag that allows users to specify a range of TCP ports to check (e.g., `8000-8080`).
- **HTTP Port Checking:** Added a new `--http-port` flag that allows users to specify one or more HTTP ports to check.
- **HTTP Port Range Checking:** Added a new `--http-port-range` flag that allows users to specify a range of HTTP ports to check (e.g., `8000-8080`).
- **HTTP URL Checking:** Added a new `--http-url` flag that allows users to specify a URL to check.
- **HTTP Response Matching:** Added a new `--http-match` flag that allows users to specify a string or regex to match in the HTTP response body.
- **JSON Output:** Added a new `--msg` flag that returns a JSON object with a detailed status and message instead of plain text.

### Improvements

- **Fixed Race Condition:** Resolved a critical race condition that occurred during concurrent health checks. A `sync.Mutex` has been implemented in `server/server.go` to protect the shared `allChecksOk` variable, ensuring thread-safe updates and accurate health check results.
- **Configurable TCP Timeout:** The previously hardcoded 5-second TCP connection timeout is now configurable via a new `--tcp-timeout` command-line flag. This allows users to fine-tune the health checker for different network environments, improving both performance and reliability.
- **Improved Modularity:** The `runChecks` function in `server/server.go` has been refactored into smaller, more focused functions (`runTcpChecks`, `runHttpChecks`, `runHttpUrlChecks`, and `runScriptChecks`), enhancing code readability and maintainability.
- **Refactored HTTP Check Logic:** Consolidated duplicated HTTP check logic into a single, more generic function to improve code quality and maintainability.
- **Enhanced Testing:** Added new test cases to `commands/flags_test.go` and `server/server_test.go` to cover all the new functionality.
- **Dependency Update:** The application's entry point in `main.go` has been updated to correctly use the `Run` method from `urfave/cli/v3`, which now requires a `context.Context`.
- **Linter Fixes:** Fixed all issues reported by `golangci-lint`.
