package options

import (
	"strings"

	"github.com/sirupsen/logrus"
)

// The options accepted by this CLI tool
type Options struct {
	Ports         []int
	HttpPorts     []int
	HttpUrl       string
	Scripts       []Script
	TcpTimeout    int
	HttpTimeout   int
	HttpMatch     string
	ScriptTimeout int
	Singleflight  bool
	ReturnJson    bool
	Listener      string
	Logger        *logrus.Logger
}

type Script struct {
	Name string
	Args []string
}

func ParseScripts(scriptStrings []string) []Script {
	rv := []Script{}
	for _, s := range scriptStrings {
		commandArr := strings.Split(s, " ")
		scriptName := commandArr[0]
		scriptParams := []string{}
		if len(commandArr) > 1 {
			scriptParams = commandArr[1:]
		}
		rv = append(rv, Script{scriptName, scriptParams})
	}
	return rv
}
