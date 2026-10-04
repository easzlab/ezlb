package snat

import (
	"reflect"
	"testing"
)

func TestSNATRulesKeepSharedBackendSeparateByVIP(t *testing.T) {
	a := SNATRule{VIP: "10.0.0.1", VIPPort: 80, BackendIP: "192.168.1.1", BackendPort: 8080, Protocol: "tcp", SnatIP: "10.0.0.1"}
	b := a
	b.VIP = "10.0.0.2"
	b.SnatIP = "10.0.0.2"
	if a.Key() == b.Key() {
		t.Fatal("different VIPs must not share an SNAT rule key")
	}
	if reflect.DeepEqual(buildRuleSpec(a), buildRuleSpec(b)) {
		t.Fatal("different VIPs must not generate the same iptables match")
	}
	if got := buildRuleSpec(a); !reflect.DeepEqual(got, []string{
		"-d", "192.168.1.1", "-p", "tcp", "--dport", "8080",
		"-m", "conntrack", "--ctdir", "ORIGINAL", "--ctorigdst", "10.0.0.1", "--ctorigdstport", "80",
		"-j", "SNAT", "--to-source", "10.0.0.1",
	}) {
		t.Fatalf("unexpected SNAT rule: %v", got)
	}
}

func TestForwardRuleAcceptsBothDirectionsForVIP(t *testing.T) {
	rule := ForwardRule{VIP: "10.0.0.1", VIPPort: 80, Protocol: "tcp"}
	if got := buildForwardRuleSpec(rule); !reflect.DeepEqual(got, []string{
		"-p", "tcp", "-m", "conntrack",
		"--ctorigdst", "10.0.0.1", "--ctorigdstport", "80", "-j", "ACCEPT",
	}) {
		t.Fatalf("unexpected FORWARD rule: %v", got)
	}
}
