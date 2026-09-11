package mcp

import (
	"os"
	"strings"
	"testing"
)

// TestSystemdUnitExposesMemoryEnvVars is the RED gate for the
// systemd unit file: BOTH Environment=MEMORY_URL=... AND
// Environment=MEMORY_APIKEY=... lines MUST be present so the
// system operator can configure IA_Recuerdo without editing the
// unit. The check is exact-line based so a future regression that
// drops one var (or uses the wrong separator) surfaces here before
// it reaches production.
func TestSystemdUnitExposesMemoryEnvVars(t *testing.T) {
	data, err := os.ReadFile("../../deploy/systemd/sourcerudder.service")
	if err != nil {
		t.Fatalf("ReadFile systemd unit: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Environment=MEMORY_URL=") {
		t.Errorf("systemd unit missing Environment=MEMORY_URL= line; full content:\n%s", content)
	}
	if !strings.Contains(content, "Environment=MEMORY_APIKEY=") {
		t.Errorf("systemd unit missing Environment=MEMORY_APIKEY= line; full content:\n%s", content)
	}
}

// TestKubernetesManifestExposesMemoryURL is the RED gate for the
// kubernetes deployment manifest: the sourcerudder container MUST
// expose MEMORY_URL under env: so the deployment can ship a
// memory integration without editing the pod spec at runtime.
func TestKubernetesManifestExposesMemoryURL(t *testing.T) {
	data, err := os.ReadFile("../../deploy/kubernetes/deployment.yaml")
	if err != nil {
		t.Fatalf("ReadFile k8s manifest: %v", err)
	}
	content := string(data)
	// Look for the env block that names MEMORY_URL. The check is
	// token-based so a future regression that quotes it differently
	// won't pass-by-accident.
	if !strings.Contains(content, "MEMORY_URL") {
		t.Errorf("kubernetes manifest does not reference MEMORY_URL anywhere; full content:\n%s", content)
	}
	// Confirm the env entry is on a line with `name:` (the standard
	// shape) and not just inside a comment.
	hasNameLine := false
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "name:") && strings.Contains(line, "MEMORY_URL") {
			hasNameLine = true
			break
		}
	}
	if !hasNameLine {
		t.Errorf("kubernetes manifest must include a `name: MEMORY_URL` env entry; full content:\n%s", content)
	}
}
