module github.com/gruntwork-io/health-check

go 1.24.0

require (
	github.com/gruntwork-io/go-commons v0.17.2
	github.com/gruntwork-io/health-checker v0.0.8
	github.com/sirupsen/logrus v1.9.4
	github.com/stretchr/testify v1.11.1
	github.com/urfave/cli/v3 v3.6.2
	golang.org/x/sync v0.19.0
)

require (
	github.com/cpuguy83/go-md2man/v2 v2.0.7 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-errors/errors v1.5.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/urfave/cli/v2 v2.27.7 // indirect
	github.com/xrash/smetrics v0.0.0-20250705151800-55b8f293f342 // indirect
	golang.org/x/sys v0.41.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/gruntwork-io/health-checker => ./
