package snat

import "strconv"

// buildRuleSpec matches both the current backend and the original VIP tuple.
// The latter keeps traffic for different VIPs and unrelated traffic separate.
func buildRuleSpec(rule SNATRule) []string {
	spec := []string{
		"-d", rule.BackendIP,
		"-p", rule.Protocol,
		"--dport", strconv.Itoa(int(rule.BackendPort)),
		"-m", "conntrack", "--ctdir", "ORIGINAL",
		"--ctorigdst", rule.VIP,
		"--ctorigdstport", strconv.Itoa(int(rule.VIPPort)),
	}
	if rule.SnatIP != "" {
		return append(spec, "-j", "SNAT", "--to-source", rule.SnatIP)
	}
	return append(spec, "-j", "MASQUERADE")
}

// buildForwardRuleSpec accepts both directions of a connection to this VIP.
// IPVS NAT traverses FORWARD in both directions; --ctorigdst scopes the rule
// to the configured virtual service even after destination NAT.
func buildForwardRuleSpec(rule ForwardRule) []string {
	return []string{
		"-p", rule.Protocol,
		"-m", "conntrack",
		"--ctorigdst", rule.VIP,
		"--ctorigdstport", strconv.Itoa(int(rule.VIPPort)),
		"-j", "ACCEPT",
	}
}
