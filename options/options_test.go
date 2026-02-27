package options

import (
	"github.com/stretchr/testify/assert"
	"os"
	"path/filepath"
	"testing"
)

func TestParseScripts(t *testing.T) {
	// Create a temporary dummy script to represent an actual executable on disk
	tmpDir := t.TempDir()
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	dummyScriptPath := filepath.Join(tmpDir, "dummy_script.bat")
	err := os.WriteFile(dummyScriptPath, []byte("echo hello"), 0755)
	assert.NoError(t, err)

	// Create a temporary script with spaces in the path
	spaceDir := filepath.Join(tmpDir, "space dir")
	err = os.Mkdir(spaceDir, 0755)
	assert.NoError(t, err)
	spaceScriptPath := filepath.Join(spaceDir, "space_script.sh")
	err = os.WriteFile(spaceScriptPath, []byte("echo hello"), 0755)
	assert.NoError(t, err)

	testCases := []struct {
		name        string
		input       []string
		expected    []Script
		expectError bool
	}{
		{
			name:        "Empty input",
			input:       []string{},
			expected:    []Script{},
			expectError: false,
		},
		{
			name:        "Single command no args",
			input:       []string{filepath.ToSlash(dummyScriptPath)},
			expected:    []Script{{Name: filepath.ToSlash(dummyScriptPath), Args: []string{}}},
			expectError: false,
		},
		{
			name:        "Single command with args",
			input:       []string{filepath.ToSlash(dummyScriptPath) + " version"},
			expected:    []Script{{Name: filepath.ToSlash(dummyScriptPath), Args: []string{"version"}}},
			expectError: false,
		},
		{
			name:        "Multiple commands",
			input:       []string{filepath.ToSlash(dummyScriptPath) + " env", filepath.ToSlash(dummyScriptPath) + " version"},
			expected:    []Script{{Name: filepath.ToSlash(dummyScriptPath), Args: []string{"env"}}, {Name: filepath.ToSlash(dummyScriptPath), Args: []string{"version"}}},
			expectError: false,
		},
		{
			name:        "Command with spaces in path",
			input:       []string{`"` + filepath.ToSlash(spaceScriptPath) + `" arg1 arg2`},
			expected:    []Script{{Name: filepath.ToSlash(spaceScriptPath), Args: []string{"arg1", "arg2"}}},
			expectError: false,
		},
		{
			name:        "Executable not found",
			input:       []string{"lskdf_non_existent_binary"},
			expected:    nil,
			expectError: true,
		},
		{
			name:        "Forbidden shell character blocked",
			input:       []string{filepath.ToSlash(dummyScriptPath) + " & rm -rf /"},
			expected:    nil,
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := ParseScripts(tc.input)
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, actual)
			}
		})
	}
}
