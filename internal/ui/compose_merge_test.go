package ui

import (
	"strings"
	"testing"
)

func TestBaseComposeDefinesService(t *testing.T) {
	content := []byte("services:\n  web:\n    image: nginx:latest\n")
	if !baseComposeDefinesService(content, "web") {
		t.Fatal("baseComposeDefinesService(web) = false, want true")
	}
	if baseComposeDefinesService(content, "cache") {
		t.Fatal("baseComposeDefinesService(cache) = true, want false")
	}
	if baseComposeDefinesService(nil, "web") {
		t.Fatal("baseComposeDefinesService(nil) = true, want false")
	}
}

func TestMergeComposeServiceFieldsPreservesCommentsAndUnrelatedKeys(t *testing.T) {
	base := []byte(`# top-level comment
services:
  web:
    image: nginx:1.25 # pin this
    restart: "no"
    networks:
      - front
  cache:
    image: redis:7
`)
	merged, err := mergeComposeServiceFields(base, "web", composeOverrideService{
		Image:   "nginx:1.27",
		Restart: "unless-stopped",
		Ports:   []string{"8080:80"},
	})
	if err != nil {
		t.Fatalf("mergeComposeServiceFields() error = %v", err)
	}
	out := string(merged)
	for _, want := range []string{"# top-level comment", "networks:", "front", "redis:7", "nginx:1.27", "unless-stopped", "8080:80"} {
		if !strings.Contains(out, want) {
			t.Fatalf("merged output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "nginx:1.25") {
		t.Fatalf("merged output still has old image:\n%s", out)
	}
}

// TestMergeComposeServiceFieldsHandlesExecFormCommand guards against a
// regression where Command was decoded strictly as a string, so a real
// override whose OWN service used Compose's other allowed command shape
// — command: ["sh", "-c", "..."] — made composeServiceFieldsFromContent's
// yaml.Unmarshal fail outright ("cannot unmarshal !!seq into string"),
// which mergeComposeCreateIntoBase treated as "could not read fields for
// service" and aborted the whole merge, even for services elsewhere in
// the same document with a perfectly normal string command.
func TestMergeComposeServiceFieldsHandlesExecFormCommand(t *testing.T) {
	content := "services:\n  web:\n    image: nginx:alpine\n    command:\n      - sh\n      - -c\n      - echo hi\n"
	fields, ok := composeServiceFieldsFromContent(content, "web")
	if !ok {
		t.Fatal("composeServiceFieldsFromContent() ok = false, want true")
	}
	if normalizeComposeCommand(fields.Command) != "sh -c echo hi" {
		t.Fatalf("normalizeComposeCommand(fields.Command) = %q, want %q", normalizeComposeCommand(fields.Command), "sh -c echo hi")
	}

	base := []byte("services:\n  web:\n    image: nginx:1.25\n")
	merged, err := mergeComposeServiceFields(base, "web", fields)
	if err != nil {
		t.Fatalf("mergeComposeServiceFields() error = %v", err)
	}
	if !strings.Contains(string(merged), "sh -c echo hi") {
		t.Fatalf("merged output missing the joined command:\n%s", merged)
	}
}

func TestMergeComposeServiceFieldsErrorsWhenServiceMissing(t *testing.T) {
	base := []byte("services:\n  web:\n    image: nginx:latest\n")
	if _, err := mergeComposeServiceFields(base, "cache", composeOverrideService{Image: "redis:7"}); err == nil {
		t.Fatal("mergeComposeServiceFields() error = nil, want error for missing service")
	}
}

func TestRemoveComposeServiceDropsOnlyTargetService(t *testing.T) {
	base := []byte(`services:
  web:
    image: nginx:latest
  cache:
    image: redis:7 # keep this one
`)
	updated, err := removeComposeService(base, "web")
	if err != nil {
		t.Fatalf("removeComposeService() error = %v", err)
	}
	out := string(updated)
	if strings.Contains(out, "nginx:latest") {
		t.Fatalf("removed service still present:\n%s", out)
	}
	if !strings.Contains(out, "redis:7") || !strings.Contains(out, "keep this one") {
		t.Fatalf("untouched service lost content/comment:\n%s", out)
	}
}

func TestRemoveComposeServiceErrorsWhenServiceMissing(t *testing.T) {
	base := []byte("services:\n  web:\n    image: nginx:latest\n")
	if _, err := removeComposeService(base, "cache"); err == nil {
		t.Fatal("removeComposeService() error = nil, want error for missing service")
	}
}

func TestComposeServiceFieldsFromContent(t *testing.T) {
	content := "services:\n  cache:\n    image: redis:7\n    restart: unless-stopped\n    ports:\n      - \"6379:6379\"\n  web:\n    image: nginx:latest\n"
	fields, ok := composeServiceFieldsFromContent(content, "cache")
	if !ok {
		t.Fatal("composeServiceFieldsFromContent() ok = false, want true")
	}
	if fields.Image != "redis:7" || fields.Restart != "unless-stopped" || len(fields.Ports) != 1 || fields.Ports[0] != "6379:6379" {
		t.Fatalf("fields = %#v, unexpected values", fields)
	}
	if _, ok := composeServiceFieldsFromContent(content, "missing"); ok {
		t.Fatal("composeServiceFieldsFromContent(missing) ok = true, want false (no such service, and not the sole one)")
	}
}
