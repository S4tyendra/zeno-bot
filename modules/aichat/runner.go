package aichat

import "os"

// ContainerCLI returns the container CLI to use, defaulting to nerdctl
func ContainerCLI() string {
	if v := os.Getenv("CONTAINER_CLI"); v != "" {
		return v
	}
	return "nerdctl"
}
