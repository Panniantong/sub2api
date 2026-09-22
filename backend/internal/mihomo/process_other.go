//go:build !linux

package mihomo

import "os/exec"

func configureChild(cmd *exec.Cmd) {}
