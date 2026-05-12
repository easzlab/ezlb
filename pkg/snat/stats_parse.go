package snat

import (
	"fmt"
	"strings"

	"github.com/coreos/go-iptables/iptables"
)

// parseStructuredSNATStat converts a single iptables Stat row into the
// rule key and stats expected by the trafficmetrics collector.
//
// We rely on go-iptables' StructuredStats() rather than parsing raw string
// columns because the column layout of `iptables -vnL` is version-dependent:
// older releases return one slice element per column, while newer ones glue
// all options (including dpt:) into a single trailing field. The structured
// API hides those differences and gives us a typed Destination + Options.
func parseStructuredSNATStat(stat iptables.Stat) (string, SNATRuleStats, bool) {
	if stat.Destination == nil || stat.Protocol == "" {
		return "", SNATRuleStats{}, false
	}

	dport := extractDPortFromOptions(stat.Options)
	if dport == "" {
		return "", SNATRuleStats{}, false
	}

	// Stat.Destination is a *net.IPNet; for /32 single-host rules we want
	// just the IP literal (matches how rules are built in buildRuleSpec).
	destination := stat.Destination.IP.String()

	ruleKey := fmt.Sprintf("%s:%s/%s", destination, dport, stat.Protocol)
	return ruleKey, SNATRuleStats{
		Packets: stat.Packets,
		Bytes:   stat.Bytes,
	}, true
}

// extractDPortFromOptions pulls the destination port from an iptables
// options string such as "tcp dpt:8080" or "udp spt:1024 dpt:53".
func extractDPortFromOptions(options string) string {
	if options == "" {
		return ""
	}
	for _, token := range strings.Fields(options) {
		if strings.HasPrefix(token, "dpt:") {
			return strings.TrimPrefix(token, "dpt:")
		}
	}
	return ""
}
