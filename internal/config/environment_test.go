package config

import (
	"os"
	"testing"
)

func TestDetectContainer_NoContainer(t *testing.T) {
	ResetContainerCache()
	// Unset all container indicators
	for _, key := range []string{"CODESPACES", "GITPOD_WORKSPACE_ID", "KUBERNETES_SERVICE_HOST", "container"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	ResetContainerCache()

	info := DetectContainer()
	// In a real container (CI, devcontainer) this might still detect via cgroup/dockerenv,
	// so we only validate the struct shape.
	if info.Detected && info.Type == "" {
		t.Error("Detected=true but Type is empty")
	}
}

func TestDetectContainer_Codespaces(t *testing.T) {
	ResetContainerCache()
	t.Setenv("CODESPACES", "true")
	ResetContainerCache()

	info := DetectContainer()
	if !info.Detected {
		t.Fatal("expected container detection for CODESPACES")
	}
	if info.Type != "Codespaces" {
		t.Errorf("expected Type=Codespaces, got %q", info.Type)
	}
}

func TestDetectContainer_Gitpod(t *testing.T) {
	ResetContainerCache()
	t.Setenv("GITPOD_WORKSPACE_ID", "abc123")
	ResetContainerCache()

	info := DetectContainer()
	if !info.Detected {
		t.Fatal("expected container detection for GITPOD_WORKSPACE_ID")
	}
	if info.Type != "Gitpod" {
		t.Errorf("expected Type=Gitpod, got %q", info.Type)
	}
}

func TestDetectContainer_Kubernetes(t *testing.T) {
	ResetContainerCache()
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	ResetContainerCache()

	info := DetectContainer()
	if !info.Detected {
		t.Fatal("expected container detection for KUBERNETES_SERVICE_HOST")
	}
	if info.Type != "Kubernetes" {
		t.Errorf("expected Type=Kubernetes, got %q", info.Type)
	}
}

func TestDetectContainer_EnvVar_Podman(t *testing.T) {
	ResetContainerCache()
	t.Setenv("container", "podman")
	ResetContainerCache()

	info := DetectContainer()
	if !info.Detected {
		t.Fatal("expected container detection for container=podman")
	}
	if info.Type != "Podman" {
		t.Errorf("expected Type=Podman, got %q", info.Type)
	}
}

func TestDetectContainer_EnvVar_Docker(t *testing.T) {
	ResetContainerCache()
	t.Setenv("container", "docker")
	ResetContainerCache()

	info := DetectContainer()
	if !info.Detected {
		t.Fatal("expected container detection for container=docker")
	}
	if info.Type != "Docker" {
		t.Errorf("expected Type=Docker, got %q", info.Type)
	}
}

func TestDetectContainer_EnvVar_Generic(t *testing.T) {
	ResetContainerCache()
	t.Setenv("container", "systemd-nspawn")
	ResetContainerCache()

	info := DetectContainer()
	if !info.Detected {
		t.Fatal("expected container detection for container=systemd-nspawn")
	}
	if info.Type != "container" {
		t.Errorf("expected Type=container, got %q", info.Type)
	}
}

func TestIsContainer_ReturnsBool(t *testing.T) {
	ResetContainerCache()
	t.Setenv("CODESPACES", "true")
	ResetContainerCache()

	if !IsContainer() {
		t.Error("expected IsContainer()=true with CODESPACES set")
	}
}

func TestContainerType_ReturnsString(t *testing.T) {
	ResetContainerCache()
	t.Setenv("CODESPACES", "true")
	ResetContainerCache()

	if ct := ContainerType(); ct != "Codespaces" {
		t.Errorf("expected ContainerType()=Codespaces, got %q", ct)
	}
}

func TestContainerType_Empty_WhenNoContainer(t *testing.T) {
	ResetContainerCache()
	for _, key := range []string{"CODESPACES", "GITPOD_WORKSPACE_ID", "KUBERNETES_SERVICE_HOST", "container"} {
		os.Unsetenv(key)
	}
	ResetContainerCache()

	info := detectContainerOnce()
	// Only check env-based detection; cgroup/dockerenv may fire in CI
	if os.Getenv("CODESPACES") == "" && os.Getenv("GITPOD_WORKSPACE_ID") == "" &&
		os.Getenv("KUBERNETES_SERVICE_HOST") == "" && os.Getenv("container") == "" {
		// If still detected, it's via cgroup or .dockerenv — that's fine
		if info.Detected && info.Type == "" {
			t.Error("Detected=true but Type is empty — should always have a label")
		}
	}
}

func TestDetectContainer_CachesResult(t *testing.T) {
	ResetContainerCache()
	t.Setenv("CODESPACES", "true")
	ResetContainerCache()

	first := DetectContainer()
	// Change env — cached result should not change
	os.Unsetenv("CODESPACES")
	second := DetectContainer()

	if first.Type != second.Type {
		t.Errorf("cache broken: first=%q second=%q", first.Type, second.Type)
	}
}

func TestDetectContainer_PriorityOrder(t *testing.T) {
	ResetContainerCache()
	// Set multiple indicators — Codespaces should win (checked first)
	t.Setenv("CODESPACES", "true")
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("container", "docker")
	ResetContainerCache()

	info := DetectContainer()
	if info.Type != "Codespaces" {
		t.Errorf("expected Codespaces to take priority, got %q", info.Type)
	}
}
