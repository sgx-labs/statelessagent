package config

import (
	"bufio"
	"os"
	"strings"
	"sync"
)

// ContainerInfo holds detected container environment details.
type ContainerInfo struct {
	Detected bool
	Type     string // "Docker", "Podman", "Kubernetes", "LXC", "containerd", "Codespaces", "Gitpod", "container"
}

// containerOnce ensures detection runs only once per process (thread-safe).
var (
	containerOnce   sync.Once
	cachedContainer ContainerInfo
)

// DetectContainer probes the runtime environment for signs of containerisation.
// Detection is informational only — it never limits functionality.
func DetectContainer() ContainerInfo {
	containerOnce.Do(func() {
		cachedContainer = detectContainerOnce()
	})
	return cachedContainer
}

// IsContainer returns true when the process appears to be running inside a container.
func IsContainer() bool {
	return DetectContainer().Detected
}

// ContainerType returns a human-friendly label for the detected container
// environment, or an empty string when no container is detected.
func ContainerType() string {
	return DetectContainer().Type
}

// ResetContainerCache clears the cached detection result (for testing).
func ResetContainerCache() {
	containerOnce = sync.Once{}
	cachedContainer = ContainerInfo{}
}

func detectContainerOnce() ContainerInfo {
	// 1. Cloud dev environments (most specific first)
	if os.Getenv("CODESPACES") != "" {
		return ContainerInfo{Detected: true, Type: "Codespaces"}
	}
	if os.Getenv("GITPOD_WORKSPACE_ID") != "" {
		return ContainerInfo{Detected: true, Type: "Gitpod"}
	}

	// 2. Kubernetes
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return ContainerInfo{Detected: true, Type: "Kubernetes"}
	}

	// 3. Explicit container env var (set by systemd-nspawn, podman, etc.)
	if v := os.Getenv("container"); v != "" {
		label := "container"
		switch strings.ToLower(v) {
		case "podman":
			label = "Podman"
		case "docker":
			label = "Docker"
		case "lxc":
			label = "LXC"
		}
		return ContainerInfo{Detected: true, Type: label}
	}

	// 4. Docker marker file
	if fileExists("/.dockerenv") {
		return ContainerInfo{Detected: true, Type: "Docker"}
	}

	// 5. cgroup-based detection (Linux only, safe no-op on other platforms)
	if ct := detectFromCgroup(); ct != "" {
		return ContainerInfo{Detected: true, Type: ct}
	}

	return ContainerInfo{}
}

// detectFromCgroup reads /proc/1/cgroup looking for container runtime markers.
func detectFromCgroup() string {
	f, err := os.Open("/proc/1/cgroup")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Limit to 100 lines — /proc/1/cgroup is typically <10 lines.
	// Prevents unbounded reads from a malicious/corrupted cgroup file.
	for i := 0; i < 100 && scanner.Scan(); i++ {
		line := strings.ToLower(scanner.Text())
		switch {
		case strings.Contains(line, "docker"):
			return "Docker"
		case strings.Contains(line, "kubepods"):
			return "Kubernetes"
		case strings.Contains(line, "lxc"):
			return "LXC"
		case strings.Contains(line, "containerd"):
			return "containerd"
		}
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
