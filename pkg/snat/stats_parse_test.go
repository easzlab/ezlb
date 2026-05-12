package snat

import (
	"net"
	"testing"

	"github.com/coreos/go-iptables/iptables"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, ipNet, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	return ipNet
}

func TestParseStructuredSNATStat_SNATWithDPort(t *testing.T) {
	stat := iptables.Stat{
		Packets:     42,
		Bytes:       1024,
		Target:      "SNAT",
		Protocol:    "tcp",
		Destination: mustCIDR(t, "10.0.0.3/32"),
		Options:     "tcp dpt:8080 to:192.168.1.1",
	}

	ruleKey, stats, ok := parseStructuredSNATStat(stat)
	if !ok {
		t.Fatal("expected SNAT stats row to be parsed")
	}
	if ruleKey != "10.0.0.3:8080/tcp" {
		t.Fatalf("expected rule key 10.0.0.3:8080/tcp, got %q", ruleKey)
	}
	if stats.Packets != 42 || stats.Bytes != 1024 {
		t.Fatalf("expected stats {Packets:42 Bytes:1024}, got %+v", stats)
	}
}

func TestParseStructuredSNATStat_MasqueradeUDP(t *testing.T) {
	stat := iptables.Stat{
		Packets:     7,
		Bytes:       512,
		Target:      "MASQUERADE",
		Protocol:    "udp",
		Destination: mustCIDR(t, "10.0.0.4/32"),
		Options:     "udp dpt:5353",
	}

	ruleKey, stats, ok := parseStructuredSNATStat(stat)
	if !ok {
		t.Fatal("expected MASQUERADE stats row to be parsed")
	}
	if ruleKey != "10.0.0.4:5353/udp" {
		t.Fatalf("expected rule key 10.0.0.4:5353/udp, got %q", ruleKey)
	}
	if stats.Packets != 7 || stats.Bytes != 512 {
		t.Fatalf("expected stats {Packets:7 Bytes:512}, got %+v", stats)
	}
}

func TestParseStructuredSNATStat_MissingDPort(t *testing.T) {
	stat := iptables.Stat{
		Packets:     1,
		Bytes:       64,
		Target:      "SNAT",
		Protocol:    "tcp",
		Destination: mustCIDR(t, "10.0.0.5/32"),
		Options:     "tcp", // no dpt: token
	}

	if _, _, ok := parseStructuredSNATStat(stat); ok {
		t.Fatal("expected stats row without dpt: to be skipped")
	}
}

func TestParseStructuredSNATStat_NilDestination(t *testing.T) {
	stat := iptables.Stat{
		Packets:  1,
		Bytes:    64,
		Target:   "SNAT",
		Protocol: "tcp",
		Options:  "tcp dpt:8080",
	}

	if _, _, ok := parseStructuredSNATStat(stat); ok {
		t.Fatal("expected stats row with nil destination to be skipped")
	}
}
