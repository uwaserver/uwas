//go:build windows

package deploy

import "os/exec"

func setStepProcessGroup(cmd *exec.Cmd) {}

func killStepProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
