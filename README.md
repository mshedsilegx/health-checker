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
| `--port` | `int` | *None* | **[One of port/script/http Required]** The port number on which a TCP connection will be attempted. Specify one or more times. |
| `--script` | `string` | *None* | **[One of port/script/http Required]** Path to a script or binary to run. Pass if it completes with a 0 exit status. Specify one or more times. |
| `--http` | `string` | *None* | **[One of port/script/http Required]** An HTTP(S) URL to probe. The check succeeds if it returns a 2xx status code. Specify one or more times. |
| `--verify-payload` | `string` | *None* | **[Optional]** A regular expression to match against the body of the HTTP(S) checks. If specified, the check only succeeds if the status code is 2xx AND the response body matches the regex. Must be specified exactly once per `--http` flag if used. |
| `--allow-insecure-tls` | `bool` | `false` | **[Optional]** Skip TLS certificate verification for HTTPS checks. Use this if you are probing endpoints with self-signed certificates or broken trust chains. |
| `--listener` | `string` | `0.0.0.0:5500` | The IP address and port on which inbound HTTP connections will be accepted. |
| `--script-timeout` | `int` | `5` | Timeout, in seconds, to wait for scripts to exit. Applies to all configured script targets. |
| `--tcp-dial-timeout` | `int` | `5` | Timeout, in seconds, for dialing TCP connections for health checks. |
| `--http-read-timeout` | `int` | `5` | Timeout, in seconds, for reading the entire HTTP request, including the body. |
| `--http-write-timeout` | `int` | `0` (Dynamic) | Timeout, in seconds, for writing the HTTP response. Dynamically scales with script timeout + 5 if set to 0. |
| `--http-idle-timeout` | `int` | `15` | Timeout, in seconds, to wait for the next request when keep-alives are enabled. |
| `--singleflight` | `bool` | `false` | Enables single flight mode, allowing concurrent health check requests to share the results of a single check pass. |
| `--detailed-status` | `bool` | `false` | Returns a detailed JSON payload indicating elapsed time and specific error messages if probes fail, instead of plain text. |
| `--log-level` | `string` | `info` | Set the log level. Must be one of: `panic`, `fatal`, `error`, `warning`, `info`, `debug`, or `trace`. |
| `--help` | `bool` | `false` | Show the help screen. |
| `--version` | `bool` | `false` | Show the program's version. |

## Examples

#### Example 1: Pure TCP Port Checking
Run a listener on port 6000 that accepts inbound HTTP connections. When the request is received, attempt to open TCP connections to ports 5432 and 3306 in parallel. If both succeed, return `HTTP 200 OK`. If either fails, return `HTTP 504 Gateway Timeout`.

```bash
health-checker --listener "0.0.0.0:5000" --port 8080 --port 80
```

#### Example 2: Mixed TCP & Script Execution
Attempt to open a TCP connection to port 5432 and simultaneously run a custom script with a maximum 10-second execution window. Ensure concurrent load balancer pings share the same script execution result (`--singleflight`).

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
