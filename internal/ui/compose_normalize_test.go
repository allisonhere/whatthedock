package ui

import (
	"strings"
	"testing"
)

func TestNormalizeComposePortsHandlesShortAndLong(t *testing.T) {
	got := normalizeComposePorts([]interface{}{
		"8080:80",
		map[string]interface{}{"target": 80, "published": 8080, "protocol": "tcp"},
		map[string]interface{}{"target": 443, "published": 8443, "protocol": "udp"},
		map[string]interface{}{"target": 9000}, // container-only: not representable
	})
	want := []string{"8080:80", "8080:80", "8443:443/udp"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("normalizeComposePorts() = %#v, want %#v", got, want)
	}

	// A Go []string literal (rather than YAML's []interface{}) works too.
	if got := normalizeComposePorts([]string{"6379:6379"}); len(got) != 1 || got[0] != "6379:6379" {
		t.Fatalf("normalizeComposePorts([]string) = %#v, want the short string passed through", got)
	}
}

func TestNormalizeComposeVolumesHandlesShortAndLong(t *testing.T) {
	got := normalizeComposeVolumes([]interface{}{
		"./data:/config",
		map[string]interface{}{"type": "bind", "source": "./x", "target": "/y", "read_only": true},
		map[string]interface{}{"type": "volume", "source": "data", "target": "/var/lib/data"},
		map[string]interface{}{"type": "volume", "target": "/anon"}, // no source: skipped
	})
	want := []string{"./data:/config", "./x:/y:ro", "data:/var/lib/data"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("normalizeComposeVolumes() = %#v, want %#v", got, want)
	}
}

// TestNormalizeComposeEnvironmentNullIsNotEmpty guards the pass-through
// shape ("environment:\n  FOO:") from decoding to the literal "FOO=<nil>".
func TestNormalizeComposeEnvironmentNullIsNotEmpty(t *testing.T) {
	got := normalizeComposeEnvironment(map[string]interface{}{"FOO": nil, "BAR": "baz"})
	want := []string{"BAR=baz", "FOO="}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("normalizeComposeEnvironment() = %#v, want %#v", got, want)
	}
}

// TestApplyOverrideFieldsFromYAMLLongSyntaxStillPopulates is the regression
// test for a real-world file using Compose's long syntax: it used to fail
// yaml.Unmarshal on the whole document, leaving every form field (not just
// ports/volumes) at its default.
func TestApplyOverrideFieldsFromYAMLLongSyntaxStillPopulates(t *testing.T) {
	content := `services:
  web:
    image: nginx:latest
    restart: unless-stopped
    ports:
      - target: 80
        published: 8080
    volumes:
      - type: bind
        source: ./data
        target: /data
`
	draft := createDraft{Mode: createModeCompose, Service: "web"}
	draft.applyOverrideFieldsFromYAML(content)

	if draft.Image != "nginx:latest" || draft.Restart != "unless-stopped" {
		t.Fatalf("Image/Restart = %q/%q, want them populated despite long-syntax ports/volumes", draft.Image, draft.Restart)
	}
	if !strings.Contains(draft.Ports, "8080:80") {
		t.Fatalf("Ports = %q, want the long-syntax port normalized in", draft.Ports)
	}
	if !strings.Contains(draft.Mounts, "./data:/data") {
		t.Fatalf("Mounts = %q, want the long-syntax volume normalized in", draft.Mounts)
	}
}
