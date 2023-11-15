//go:build !android

package checkfw

import (
	"os"

	"github.com/coreos/go-iptables/iptables"
	"github.com/google/nftables"
)

const (
	// UNKNOWN is the default value for the firewall type for unknown firewall type
	UNKNOWN FWType = iota
	// IPTABLES is the value for the iptables firewall type
	IPTABLES
	// NFTABLES is the value for the nftables firewall type
	NFTABLES
)

// SKIP_NFTABLES_ENV is the environment variable to skip nftables check
const SKIP_NFTABLES_ENV = "NB_SKIP_NFTABLES_CHECK"

// FWType is the type for the firewall type
type FWType int

// Check returns the firewall type based on common lib checks. It returns UNKNOWN if no firewall is found.
func Check() FWType {
	nf := nftables.Conn{}
	if _, err := nf.ListChains(); err == nil && os.Getenv(SKIP_NFTABLES_ENV) != "true" {
		return NFTABLES
	}

	ip, err := iptables.NewWithProtocol(iptables.ProtocolIPv4)
	if err != nil {
		return UNKNOWN
	}
	if isIptablesClientAvailable(ip) {
		return IPTABLES
	}

	return UNKNOWN
}

func isIptablesClientAvailable(client *iptables.IPTables) bool {
	_, err := client.ListChains("filter")
	return err == nil
}
