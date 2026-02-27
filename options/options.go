package options

import (
	"encoding/csv"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"
)

// Options is the central configuration registry for the health-checker application.
// It maps the command-line flags into an internal structured format passed directly
// to the server subsystems, decoupling the HTTP/TCP execution logic from the CLI framework.
type Options struct {
	Ports            []string
	Scripts          []Script
	HttpChecks       []HttpCheck
	ScriptTimeout    int
	HttpReadTimeout  int
	HttpWriteTimeout int
	HttpIdleTimeout  int
	TcpDialTimeout   int
	HttpDialTimeout  int
	Singleflight     bool
	DetailedStatus   bool
	AllowInsecureTLS bool
	Listener         string
	Logger           *logrus.Logger
}

type Script struct {
	Name string
	Args []string
}

type HttpCheck struct {
	Url           string
	VerifyPayload string
}

// allowedScriptPattern enforces that scripts only contain safe alphanumeric characters,
// directory separators, dashes, underscores, spaces, dots, colons, and quotes.
var allowedScriptPattern = regexp.MustCompile(`^[a-zA-Z0-9/\-_ .":]+$`)

func ParseScripts(scriptStrings []string) ([]Script, error) {
	rv := []Script{}
	for _, s := range scriptStrings {
		// Basic sanitization: Ensure string only contains allowed characters
		if !allowedScriptPattern.MatchString(s) {
			return nil, fmt.Errorf("script contains forbidden characters for safety: %s", s)
		}

		// Use a CSV reader to correctly handle spaces inside quotes
		// This ensures paths like `"C:\Program Files\script.bat" arg1` parse correctly.
		r := csv.NewReader(strings.NewReader(s))
		r.Comma = ' '
		r.TrimLeadingSpace = true

		commandArr, err := r.Read()
		if err != nil || len(commandArr) == 0 {
			// Fallback to simple split if CSV parsing fails
			commandArr = strings.Split(s, " ")
		}

		scriptName := commandArr[0]

		// Strictly enforce that the provided script is a valid file that actually exists
		// on disk, preventing direct execution of global system binaries like `ping` or `rm`
		info, err := os.Stat(scriptName)
		if err != nil || info.IsDir() {
			return nil, fmt.Errorf("script provided is not a valid or accessible file on disk: %s", scriptName)
		}

		scriptParams := []string{}
		if len(commandArr) > 1 {
			scriptParams = commandArr[1:]
		}
		rv = append(rv, Script{scriptName, scriptParams})
	}
	return rv, nil
}
