package commands

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v3"
)

func TestAllCliOptionsEmpty(t *testing.T) {
	cmd := &cli.Command{
		Name: "test",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "foo"},
		},
	}

	// Since urfave/cli/v3 command requires a context, we can test allCliOptionsEmpty
	// by simulating a run
	var wasEmpty bool
	cmd.Action = func(ctx context.Context, c *cli.Command) error {
		wasEmpty = allCliOptionsEmpty(c)
		return nil
	}

	// Run with no flags
	err := cmd.Run(context.Background(), []string{"test"})
	assert.NoError(t, err)
	assert.True(t, wasEmpty)

	// Run with a flag
	err = cmd.Run(context.Background(), []string{"test", "--foo", "bar"})
	assert.NoError(t, err)
	assert.False(t, wasEmpty)
}

func TestMissingParamError(t *testing.T) {
	err := MissingParam("test-flag")
	assert.Equal(t, "Missing required parameter --test-flag", err.Error())
}

func TestIsDebugMode(t *testing.T) {
	_ = os.Setenv(ENV_VAR_NAME_DEBUG_MODE, "true")
	assert.True(t, isDebugMode())

	_ = os.Setenv(ENV_VAR_NAME_DEBUG_MODE, "false")
	assert.False(t, isDebugMode())

	_ = os.Unsetenv(ENV_VAR_NAME_DEBUG_MODE)
	assert.False(t, isDebugMode())
}
