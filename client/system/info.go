package system

import (
	"context"
	"strings"

	"google.golang.org/grpc/metadata"

	"github.com/netbirdio/netbird/version"
)

// DeviceNameCtxKey context key for device name
const DeviceNameCtxKey = "deviceName"

// OsVersionCtxKey context key for operating system version
const OsVersionCtxKey = "OsVersion"

// OsNameCtxKey context key for operating system name
const OsNameCtxKey = "OsName"

// Info is an object that contains machine information
// Most of the code is taken from https://github.com/matishsiao/goInfo
type Info struct {
	GoOS               string
	Kernel             string
	Core               string
	Platform           string
	OS                 string
	OSVersion          string
	Hostname           string
	CPUs               int
	WiretrusteeVersion string
	UIVersion          string
}

// extractUserAgent extracts Netbird's agent (client) name and version from the outgoing context
func extractUserAgent(ctx context.Context) string {
	md, hasMeta := metadata.FromOutgoingContext(ctx)
	if hasMeta {
		agent, ok := md["user-agent"]
		if ok {
			nbAgent := strings.Split(agent[0], " ")[0]
			if strings.HasPrefix(nbAgent, "netbird") {
				return nbAgent
			}
			return ""
		}
	}
	return ""
}

// extractDeviceName extracts device name from context or returns the default system name
func extractDeviceName(ctx context.Context, defaultName string) string {
	v, ok := ctx.Value(DeviceNameCtxKey).(string)
	if !ok {
		return defaultName
	}
	return v
}

// GetDesktopUIUserAgent returns the Desktop ui user agent
func GetDesktopUIUserAgent() string {
	return "netbird-desktop-ui/" + version.NetbirdVersion()
}
