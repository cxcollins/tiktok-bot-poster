package tiktok

import (
	"fmt"
	"os"
	"os/exec"
)

// RunPython execs a python script and streams its stdout/stderr.
// Returns an error if the script exits non-zero.
func RunPython(script string) error {
	cmd := exec.Command(pythonBin(), script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", script, err)
	}
	return nil
}

// Assemble runs assemble.py to build slides from story.json.
func Assemble() error {
	return RunPython("assemble.py")
}

// Post runs post.py to upload the slides to TikTok.
func Post() error {
	return RunPython("post.py")
}

func pythonBin() string {
	if v := os.Getenv("PYTHON_BIN"); v != "" {
		return v
	}
	return "python3"
}
