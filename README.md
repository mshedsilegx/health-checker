# health-checker

## Application Overview and Objectives

`health-checker` is a lightweight, cross-platform daemon designed to accurately report the true health of a server by executing multi-layered concurrent probes when pinged by a Load Balancer.

A common challenge when configuring cloud infrastructure (such as Auto Scaling Groups) is that Load Balancers are traditionally limited to evaluating a single TCP port or a single HTTP endpoint to determine instance health. However, modern applications often run multiple services per instance, and validating the system requires verifying multiple database connections, internal caches, and background processes. 

`health-checker` solves this by acting as an HTTP facade. When it receives a health check ping from a Load Balancer, it simultaneously executes an array of user-defined TCP connection attempts and custom executable scripts. 

**Objectives:**
- **Aggregate Health Polling:** If **all** TCP ports accept connections and **all** scripts execute successfully (exit code 0), it returns an `HTTP 200 OK`.
- **Early Short-Circuiting:** If **any** single probe fails, it immediately aborts the remaining active probes and swiftly returns an `HTTP 504 Gateway Timeout`.
- **Performance at Scale:** Prevents resource exhaustion from request bursts via Singleflight caching, ensuring concurrent load balancer pings share probe execution states rather than duplicating expensive background processes.

## Architecture and Design Choices

The application is written in Go to maximize concurrency, safety, and cross-platform native execution. 

- **Concurrency Model:** Uses `goroutines` and `sync.WaitGroup` to launch all TCP and script probes entirely in parallel, maximizing speed and minimizing latency.
- **Master Cancellation Context:** An early short-circuiting mechanism uses `context.WithCancel`. If a single health check branch fails, the `masterCancel()` function is triggered, immediately terminating all other running system processes and TCP dials safely.
- **Strict Input Sanitization:** Command-line arguments for scripts are strictly evaluated against an allowlist pattern (only alphanumeric chars). It uses robust parsing to properly handle file paths containing spaces. The application also actively guarantees only existing, valid binary files on disk are executed, mitigating command injection vulnerabilities (it will refuse to execute pure shell built-ins without a valid absolute path).
- **Singleflight Pattern:** Integrated using `golang.org/x/sync/singleflight`, if multiple identical health check HTTP requests arrive simultaneously (a common occurrence with multi-AZ load balancers), `health-checker` executes the expensive underlying scripts exactly *once*, caching and broadcasting the shared result to all waiting requests.
- **Graceful Timeouts:** Independent read, write, idle, TCP, and Script process timeouts are dynamically mapped and bounded to prevent hanging routines.

### Understanding Singleflight (`--singleflight`)

The Singleflight pattern is a concurrency mechanism designed to prevent resource exhaustion from concurrent request bursts. When enabled via the `--singleflight` flag, it ensures that only one execution of your health checks occurs at any given time, regardless of how many concurrent inbound requests the server receives.

**How it works:**
1. A health check request arrives from the Load Balancer. The `health-checker` begins running the configured TCP probes and scripts.
2. While those initial checks are still running, 5 more health check requests arrive simultaneously from other subnets or load balancer nodes.
3. Instead of spawning 5 new duplicate sets of scripts and TCP dials, `health-checker` holds the 5 new requests in a waiting state.
4. When the original check finishes, the single result (e.g., `HTTP 200 OK`) is instantly broadcasted and returned to all 6 waiting requests simultaneously.

**When to use it:**
- **Heavy Script Executions:** If your `--script` arguments trigger CPU or memory-intensive actions (e.g., launching Java/JVM binaries, querying large internal databases, or running heavy Python scripts), running them concurrently could inadvertently cause a self-inflicted Denial of Service (DoS) on your instance. Singleflight prevents this resource exhaustion.
- **Multi-AZ Load Balancing:** In environments like AWS where an instance might be registered to multiple Target Groups, or probed by multiple Load Balancer nodes across Availability Zones simultaneously, the instance might receive a tight burst of concurrent health check pings exactly at the same interval. `--singleflight` ensures this burst translates to only a single system-level check on the machine.

**When NOT to use it:**
- If your checks are incredibly lightweight (e.g., only pure TCP port checks on localhost) and you require strictly independent, un-cached validation execution for every single individual HTTP request.

## Dependencies

The `health-checker` is fully statically compiled via Go, meaning it has zero system-level dependencies for execution.

**Go Module Dependencies:**
- `github.com/urfave/cli/v3` - Modern CLI framework used for parsing command-line flags and handling configuration routing.
- `golang.org/x/sync/singleflight` - Used to de-duplicate parallel inbound health checks.
- `github.com/sirupsen/logrus` - Used for structured, leveled logging.
- `github.com/gruntwork-io/go-commons` - Gruntwork's shared library for enhanced error stack tracing.

## Command Line Arguments

`health-checker [options]`

| Option | Type | Default | Description |
| ------ | ---- | ------- | ----------- |
| `--port` | `string` | *None* | **[One of port/script/http Required]** The port number on which a TCP connection will be attempted. Can be a simple port (e.g., `8000`) for a local check on `0.0.0.0`, or an `ip:port` (e.g. `127.0.0.1:8000`) as well as a `host:port` (e.g., `www.somehost.net:9000`) for a remote check. Specify one or more times. |
| `--script` | `string` | *None* | **[One of port/script/http Required]** Path to a script or binary to run. Pass if it completes with a 0 exit status. Specify one or more times. |
| `--http` | `string` | *None* | **[One of port/script/http Required]** An HTTP(S) URL to probe. The check succeeds if it returns a 2xx status code. Specify one or more times. |
| `--verify-payload` | `string` | *None* | **[Optional]** A regular expression to match against the body of the HTTP(S) checks. If specified, the check only succeeds if the status code is 2xx AND the response body matches the regex. Must be specified exactly once per `--http` flag if used. |
| `--allow-insecure-tls` | `bool` | `false` | **[Optional]** Skip TLS certificate verification for HTTPS checks. Use this if you are probing endpoints with self-signed certificates or broken trust chains. |
| `--listener` | `string` | `0.0.0.0:5500` | The IP address and port on which inbound HTTP connections will be accepted. |
| `--script-timeout` | `int` | `5` | Timeout, in seconds, to wait for scripts to exit. Applies to all configured script targets. |
| `--tcp-dial-timeout` | `int` | `5` | Timeout, in seconds, for dialing TCP connections for health checks. |
| `--http-dial-timeout` | `int` | `5` | Timeout, in seconds, for dialing HTTP(S) connections for health checks. |
| `--http-read-timeout` | `int` | `5` | Timeout, in seconds, for reading the entire HTTP request, including the body. |
| `--http-write-timeout` | `int` | `0` (Dynamic) | Timeout, in seconds, for writing the HTTP response. Dynamically scales with script timeout + 5 if set to 0. |
| `--http-idle-timeout` | `int` | `15` | Timeout, in seconds, to wait for the next request when keep-alives are enabled. |
| `--singleflight` | `bool` | `false` | Enables single flight mode, allowing concurrent health check requests to share the results of a single check pass. |
| `--detailed-status` | `bool` | `false` | Returns a detailed JSON payload indicating elapsed time and specific error messages if probes fail, instead of plain text. |
| `--log-level` | `string` | `info` | Set the log level. Must be one of: `panic`, `fatal`, `error`, `warning`, `info`, `debug`, or `trace`. |
| `--help` | `bool` | `false` | Show the help screen. |
| `--version` | `bool` | `false` | Show the program's version. |

## Understanding Timeouts

Because `health-checker` is intended to act as an edge facade over critical and potentially long-running dependencies, safely managing connection limits and preventing resource starvation is extremely important. There are two primary categories of timeouts handled by the daemon:

### 1. Inbound Connection Timeouts (From the Load Balancer to `health-checker`)

These timeouts dictate how long the daemon will allow the calling Load Balancer to hold an open connection while waiting for a response. By default, they are tuned aggressively to protect against Slowloris-style denial-of-service attacks.

*   `--http-read-timeout` (Default: `5s`): The maximum duration allowed for reading the *entire* incoming HTTP request (headers + body) from the Load Balancer. If you have a slow network or your LB sends large payloads, you may need to increase this.
*   `--http-write-timeout` (Default: Dynamic): The maximum duration the `health-checker` is allowed to take to *write* the response back to the Load Balancer. **Crucially, this must be longer than your longest running check**, otherwise the connection will close before the check finishes. If left at `0` (the default), `health-checker` automatically sets this to: `--script-timeout + 5 seconds`.
*   `--http-idle-timeout` (Default: `15s`): When Keep-Alives are enabled, this defines how long the server will wait for a subsequent request on an already established connection before closing it.

### 2. Outbound Probe Timeouts (From `health-checker` to your application)

These timeouts dictate how long the daemon will wait for your underlying services (databases, web servers, or custom scripts) to respond.

*   `--script-timeout` (Default: `5s`): Applies exclusively to `--script` checks. The absolute maximum time the shell process is allowed to run. If the process takes longer, `health-checker` automatically sends a `SIGKILL` to forcefully terminate the process tree, protecting against frozen scripts. If you have a slow-booting JVM or large queries, you **must** increase this.
*   `--tcp-dial-timeout` (Default: `5s`): Applies exclusively to `--port` checks. Defines the maximum duration the daemon will wait during the initial TCP handshake (SYN/ACK).
*   `--http-dial-timeout` (Default: `5s`): Applies exclusively to `--http` checks. Defines the maximum duration the daemon will wait for an initial HTTP(S) connection to the target URL to be established and verified. Useful when checking slow/remote API endpoints.

**Note on Early Short-Circuiting:** If you define *multiple* checks (e.g. 5 ports, 2 scripts), and one port instantly fails to connect (e.g. `Connection Refused`), `health-checker` does not wait for the other scripts or ports to hit their timeouts. The master context is instantly cancelled, all other checks are aborted, and a `504 Gateway Timeout` is returned immediately.

## Examples

#### Example 1: TCP Port Checking (Local and Remote)
Run a listener on port 5000 that accepts inbound HTTP connections. When the request is received, attempt to open TCP connections to local port 8080 and remote port 443 on example.com in parallel. If both succeed, return `HTTP 200 OK`. If either fails, return `HTTP 504 Gateway Timeout`.

```bash
health-checker --listener "0.0.0.0:5000" --port 8080 --port 8181 --port example.com:443 --port 127.0.0.1:8080
```

Ports can also be specified as a list:
```bash
health-checker --listener "0.0.0.0:5000" --port 8080,443,80
```

#### Example 2: Mixed TCP & Script Execution
Attempt to open a TCP connection to port 8080 and simultaneously run a custom script with a maximum 10-second execution window. Ensure concurrent load balancer pings share the same script execution result (`--singleflight`).

```bash
health-checker --listener "0.0.0.0:5000" --port 8080 --script "/path/to/script.sh" --script-timeout 10 --singleflight
```

#### Example 3: Multiple Scripts with Detailed Debug JSON
Execute two separate cluster verification scripts. If either script exits with a non-zero code, output a detailed JSON object indicating exactly which script failed, the elapsed time, and the captured `stderr` output to assist with Load Balancer debugging.

```bash
health-checker --listener "0.0.0.0:5000" \
  --script "/usr/local/bin/exhibitor-check.sh" \
  --script "/usr/local/bin/zk-check.sh" \
  --detailed-status
```

*Example of `--detailed-status` output on failure:*
```json
{
  "status": "At least one health check failed",
  "elapsed_time": "5.002s",
  "errors": [
    "Script /usr/local/bin/zk-check.sh failed: exit status 1 (Output: Connection refused)"
  ]
}
```

#### Example 4: HTTP Endpoint Polling with Regex Payload Validation
Ensure that multiple local background services are reachable and actively responding with specific payloads before marking the node as healthy. The `--verify-payload` flag maps positionally (1-to-1) to the `--http` flags.

```bash
health-checker --listener "0.0.0.0:5000" \
  --http "https://localhost:8443/api/v1/status" \
  --verify-payload "\"status\":\s*\"READY\"" \
  --http "http://localhost:8080/api/v2/health" \
  --verify-payload "\"OK\""
```
