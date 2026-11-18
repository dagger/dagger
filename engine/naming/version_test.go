package naming

import (
	"strings"
	"testing"
)

func TestDictionaryFor(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    *Dictionary
	}{
		{"", Latest},
		{"v1.0.0", Initial},
		{"1.0.0", Initial},
		{"v1.0.0-beta.15", Initial},
		{"v1.0.0-dev-123+abc", Initial},
		{"v1.2.3", Initial},
		{"v0.19.4", Initial},
		{"not-a-version", Initial},
	} {
		if got := DictionaryFor(tc.version); got != tc.want {
			t.Errorf("DictionaryFor(%q) = %p, want %p", tc.version, got, tc.want)
		}
	}
}

// frozenInitial is the Initial dictionary as released with FirstVersion: each
// entry's spelling and capitalized form.
const frozenInitial = `
ACL Acl
API Api
ASCII Ascii
CPU Cpu
CSS Css
DNS Dns
EOF Eof
GUID Guid
HTML Html
HTTP Http
HTTPS Https
ID Id
IP Ip
JSON Json
LHS Lhs
QPS Qps
RAM Ram
RHS Rhs
RPC Rpc
SLA Sla
SMTP Smtp
SQL Sql
SSH Ssh
TCP Tcp
TLS Tls
TTL Ttl
UDP Udp
UI Ui
UID Uid
UUID Uuid
URI Uri
URL Url
UTF8 Utf8
VM Vm
XML Xml
XMPP Xmpp
XSRF Xsrf
XSS Xss
FS Fs
SDK Sdk
LLM Llm
GPU Gpu
VCS Vcs
SHA Sha
MCP Mcp
OCI Oci
SSHFS Sshfs
E2E E2e
CLI Cli
TUI Tui
IO Io
PR Pr
EC2 Ec2
MD5 Md5
GitHub GitHub
GitLab GitLab
OAuth Oauth
IPv4 Ipv4
IPv6 Ipv6
iOS Ios
macOS MacOS
gRPC Grpc
GraphQL GraphQL
3D 3D
2D 2D
K8s K8s
K3s K3s
`

// A released dictionary must never change: every module on it would be
// renamed. Adding, removing or respelling an entry goes in a new Dictionary,
// released at a new engine version in version.go's releases.
func TestInitialDictionaryFrozen(t *testing.T) {
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(frozenInitial), "\n") {
		spelling, capitalized, _ := strings.Cut(line, " ")
		want[spelling] = capitalized
	}
	got := map[string]string{}
	for _, term := range Initial.Terms() {
		got[term.Spelling] = term.Capitalized
	}

	const fix = "released dictionaries are frozen; add the change to a new Dictionary in version.go's releases instead"
	for spelling, capitalized := range got {
		switch frozen, ok := want[spelling]; {
		case !ok:
			t.Errorf("Initial gained %q: %s", spelling, fix)
		case frozen != capitalized:
			t.Errorf("Initial changed %q's capitalized form from %q to %q: %s", spelling, frozen, capitalized, fix)
		}
	}
	for spelling := range want {
		if _, ok := got[spelling]; !ok {
			t.Errorf("Initial lost %q: %s", spelling, fix)
		}
	}
}
