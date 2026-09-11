package ui

import (
	"testing"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/domain"
)

// TestParseCreatePortsRealisticDockerForms is the regression test for a
// confirmed bug: parseCreatePorts used naive strings.Count(part, ":") >= 2
// colon-counting to decide whether a host IP prefix was present, then cut
// at the *first* colon in the whole entry to split it off. For a bracketed
// IPv6 address the address itself contains colons before its own closing
// "]", so the first colon in "[::1]:8080:80" falls inside the address
// (right after "["), producing garbage (hostIP="[", and everything after
// unparseable). IPv4 forms were never affected (dotted, no colons), which
// is why this went unnoticed.
func TestParseCreatePortsRealisticDockerForms(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  app.PortBinding
	}{
		{
			name:  "host:container",
			value: "8080:80",
			want:  app.PortBinding{HostIP: "", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
		},
		{
			name:  "ipv4:host:container",
			value: "127.0.0.1:8080:80",
			want:  app.PortBinding{HostIP: "127.0.0.1", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
		},
		{
			name:  "host:container/udp",
			value: "8080:80/udp",
			want:  app.PortBinding{HostIP: "", HostPort: 8080, ContainerPort: 80, Protocol: "udp"},
		},
		{
			name:  "ipv4:host:container/udp",
			value: "127.0.0.1:8080:80/udp",
			want:  app.PortBinding{HostIP: "127.0.0.1", HostPort: 8080, ContainerPort: 80, Protocol: "udp"},
		},
		{
			name:  "bracketed loopback ipv6",
			value: "[::1]:8080:80",
			want:  app.PortBinding{HostIP: "::1", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
		},
		{
			name:  "bracketed any-address ipv6",
			value: "[::]:8080:80",
			want:  app.PortBinding{HostIP: "::", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
		},
		{
			name:  "bracketed full ipv6 literal",
			value: "[2001:db8::1]:8080:80",
			want:  app.PortBinding{HostIP: "2001:db8::1", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
		},
		{
			name:  "bracketed ipv6 with udp",
			value: "[::1]:8080:80/udp",
			want:  app.PortBinding{HostIP: "::1", HostPort: 8080, ContainerPort: 80, Protocol: "udp"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCreatePorts(tt.value)
			if err != nil {
				t.Fatalf("parseCreatePorts(%q) error = %v", tt.value, err)
			}
			if len(got) != 1 {
				t.Fatalf("parseCreatePorts(%q) = %#v, want exactly 1 binding", tt.value, got)
			}
			if got[0] != tt.want {
				t.Fatalf("parseCreatePorts(%q) = %#v, want %#v", tt.value, got[0], tt.want)
			}
		})
	}
}

// TestParseCreatePortsRejectsMalformedIPv6 checks that a broken bracketed
// address produces a clear error instead of a confusing downstream port
// parse failure or, worse, a wrong-but-valid-looking binding.
func TestParseCreatePortsRejectsMalformedIPv6(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "unterminated bracket", value: "[::1:8080:80"},
		{name: "missing colon after bracket", value: "[::1]8080:80"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseCreatePorts(tt.value); err == nil {
				t.Fatalf("parseCreatePorts(%q) error = nil, want a clear error", tt.value)
			}
		})
	}
}

// TestParseCreatePortsHostContainerNotSwapped guards against a host/
// container port transposition regression specifically for the bracketed
// IPv6 path, where the fix touches the exact code that decides where the
// address prefix ends and the host:container pair begins.
func TestParseCreatePortsHostContainerNotSwapped(t *testing.T) {
	got, err := parseCreatePorts("[::1]:9090:81")
	if err != nil {
		t.Fatalf("parseCreatePorts() error = %v", err)
	}
	if len(got) != 1 || got[0].HostPort != 9090 || got[0].ContainerPort != 81 {
		t.Fatalf("parseCreatePorts() = %#v, want HostPort=9090 ContainerPort=81 (not swapped)", got)
	}
}

// TestFormatPortHostIPBracketsIPv6 checks the formatter side directly:
// domain/app port structs store a bare "::1"-style address (no brackets —
// that's what a real Docker inspect reports), and the create/edit/paste
// text fields must bracket it so the value they produce is both
// unambiguous to read and actually re-parseable by parseCreatePorts.
func TestFormatPortHostIPBracketsIPv6(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want string
	}{
		{name: "empty", ip: "", want: ""},
		{name: "any-interface default omitted", ip: "0.0.0.0", want: ""},
		{name: "ipv4 unbracketed", ip: "127.0.0.1", want: "127.0.0.1"},
		{name: "ipv6 loopback bracketed", ip: "::1", want: "[::1]"},
		{name: "ipv6 any-address bracketed", ip: "::", want: "[::]"},
		{name: "full ipv6 literal bracketed", ip: "2001:db8::1", want: "[2001:db8::1]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatPortHostIP(tt.ip); got != tt.want {
				t.Fatalf("formatPortHostIP(%q) = %q, want %q", tt.ip, got, tt.want)
			}
		})
	}
}

// TestFormatDraftPortsThenParseCreatePortsRoundTripsIPv6 is the end-to-end
// check for the Clone/Edit/Replicate prefill path: a container's real
// (unbracketed) IPv6 host binding, once shown in the editable Ports text
// field and parsed back on confirm, must come out identical — not corrupt,
// not silently dropped, not collapsed into the wrong fields.
func TestFormatDraftPortsThenParseCreatePortsRoundTripsIPv6(t *testing.T) {
	ports := []domain.Port{
		{IP: "::1", Private: 80, Public: 8080, Type: "tcp"},
		{IP: "0.0.0.0", Private: 443, Public: 8443, Type: "tcp"},
	}
	text := formatDraftPorts(ports)

	got, err := parseCreatePorts(text)
	if err != nil {
		t.Fatalf("parseCreatePorts(%q) error = %v", text, err)
	}
	if len(got) != 2 {
		t.Fatalf("parseCreatePorts(%q) = %#v, want 2 bindings", text, got)
	}
	byContainerPort := map[uint16]app.PortBinding{}
	for _, p := range got {
		byContainerPort[p.ContainerPort] = p
	}
	if b := byContainerPort[80]; b.HostIP != "::1" || b.HostPort != 8080 {
		t.Fatalf("round-tripped ipv6 binding = %#v, want HostIP=::1 HostPort=8080 (text was %q)", b, text)
	}
	if b := byContainerPort[443]; b.HostIP != "" || b.HostPort != 8443 {
		t.Fatalf("round-tripped 0.0.0.0 binding = %#v, want HostIP empty HostPort=8443 (text was %q)", b, text)
	}
}

// TestFormatSpecPortsThenParseCreatePortsRoundTripsIPv6 is the paste
// path's counterpart — same round trip, starting from app.PortBinding
// (what a paste plan's spec carries) instead of domain.Port.
func TestFormatSpecPortsThenParseCreatePortsRoundTripsIPv6(t *testing.T) {
	ports := []app.PortBinding{{HostIP: "2001:db8::1", HostPort: 9000, ContainerPort: 9000, Protocol: "tcp"}}
	text := formatSpecPorts(ports)

	got, err := parseCreatePorts(text)
	if err != nil {
		t.Fatalf("parseCreatePorts(%q) error = %v", text, err)
	}
	if len(got) != 1 || got[0] != ports[0] {
		t.Fatalf("parseCreatePorts(%q) = %#v, want %#v", text, got, ports[0])
	}
}
