package routemanager

import (
	"context"
	"fmt"

	firewall "github.com/netbirdio/netbird/client/firewall/manager"
	"github.com/netbirdio/netbird/iface"
)

func newServerRouter(context.Context, *iface.WGIface, firewall.Manager) (serverRouter, error) {
	return nil, fmt.Errorf("server route not supported on this os")
}
