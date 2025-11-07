# Changelog

## 2025-11-07

### Improvements

- **Fixed Race Condition:** Resolved a critical race condition that occurred during concurrent health checks. A `sync.Mutex` has been implemented in `server/server.go` to protect the shared `allChecksOk` variable, ensuring thread-safe updates and accurate health check results.
- **Configurable TCP Timeout:** The previously hardcoded 5-second TCP connection timeout is now configurable via a new `--tcp-timeout` command-line flag. This allows users to fine-tune the health checker for different network environments, improving both performance and reliability.
- **Improved Modularity:** The `runChecks` function in `server/server.go` has been refactored into smaller, more focused functions (`runTcpChecks` and `runScriptChecks`), enhancing code readability and maintainability.
- **Enhanced Testing:** A new test case, `TestRaceCondition`, has been added to `server/server_test.go` to specifically validate the fix for the race condition.
- **Dependency Update:** The application's entry point in `main.go` has been updated to correctly use the `Run` method from `urfave/cli/v3`, which now requires a `context.Context`.

### Code Review Summary

The changes were reviewed and found to be **mostly correct**. The solution successfully addresses the primary goals of fixing the race condition and making the TCP timeout configurable. The code quality is high, and the addition of a specific test case for the race condition was noted as a best practice. A minor out-of-scope change related to the `context.Context` was identified but deemed acceptable as a beneficial architectural improvement.
