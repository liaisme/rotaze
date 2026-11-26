package rotator

import (
	"context"
	"net/netip"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/control"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/common/x/list"
)

// noopPlatformInterface supplies a non-netlink default interface monitor.
// sing-tun's netlink implementation binds routing multicast groups that fail
// with EINVAL in containers lacking CAP_NET_ADMIN/CAP_NET_RAW. Every platform
// gate answers false except UsePlatformDefaultInterfaceMonitor, which routes
// network manager construction to CreateDefaultInterfaceMonitor below.
type noopPlatformInterface struct{}

var _ adapter.PlatformInterface = (*noopPlatformInterface)(nil)

func (noopPlatformInterface) Initialize(adapter.NetworkManager) error { return nil }

func (noopPlatformInterface) UsePlatformAutoDetectInterfaceControl() bool { return false }

func (noopPlatformInterface) AutoDetectInterfaceControl(int) error { return nil }

func (noopPlatformInterface) UsePlatformInterface() bool { return false }

func (noopPlatformInterface) OpenInterface(*tun.Options, option.TunPlatformOptions) (tun.Tun, error) {
	return nil, nil
}

func (noopPlatformInterface) ProcessPlatformOptions(option.TunPlatformOptions) error { return nil }

func (noopPlatformInterface) UsePlatformDefaultInterfaceMonitor() bool { return true }

func (noopPlatformInterface) CreateDefaultInterfaceMonitor(logger.Logger) tun.DefaultInterfaceMonitor {
	return noopDefaultInterfaceMonitor{}
}

func (noopPlatformInterface) UsePlatformNetworkInterfaces() bool { return false }

func (noopPlatformInterface) NetworkInterfaces() ([]adapter.NetworkInterface, error) {
	return nil, nil
}

func (noopPlatformInterface) UnderNetworkExtension() bool { return false }

func (noopPlatformInterface) NetworkExtensionIncludeAllNetworks() bool { return false }

func (noopPlatformInterface) ClearDNSCache() {}

func (noopPlatformInterface) RequestPermissionForWIFIState() error { return nil }

func (noopPlatformInterface) ReadWIFIState(context.Context) adapter.WIFIState {
	return adapter.WIFIState{}
}

func (noopPlatformInterface) UsePlatformConnectionOwnerFinder() bool { return false }

func (noopPlatformInterface) FindConnectionOwner(*adapter.FindConnectionOwnerRequest) (*adapter.ConnectionOwner, error) {
	return nil, nil
}

func (noopPlatformInterface) UsePlatformWIFIMonitor() bool { return false }

func (noopPlatformInterface) UsePlatformNotification() bool { return false }

func (noopPlatformInterface) SendNotification(*adapter.Notification) error { return nil }

func (noopPlatformInterface) CancelNotification(string, int32) error { return nil }

func (noopPlatformInterface) MyInterfaceAddress() []netip.Addr { return nil }

func (noopPlatformInterface) UsePlatformNeighborResolver() bool { return false }

func (noopPlatformInterface) StartNeighborMonitor(adapter.NeighborUpdateListener) error { return nil }

func (noopPlatformInterface) CloseNeighborMonitor(adapter.NeighborUpdateListener) error { return nil }

func (noopPlatformInterface) UsePlatformShell() bool { return false }

func (noopPlatformInterface) CheckPlatformShell() error { return nil }

func (noopPlatformInterface) OpenShellSession(*adapter.PlatformUser, string, []string, string, int32, int32) (adapter.ShellSession, error) {
	return nil, nil
}

func (noopPlatformInterface) LookupUser(string) (*adapter.PlatformUser, error) { return nil, nil }

func (noopPlatformInterface) LookupSFTPServer() (string, error) { return "", nil }

func (noopPlatformInterface) ReadSystemSSHHostKey() ([]byte, error) { return nil, nil }

func (noopPlatformInterface) TailscaleHostname() string { return "" }

func (noopPlatformInterface) UsePlatformBridge() bool { return false }

func (noopPlatformInterface) CreateBridge(adapter.BridgeOptions) (adapter.BridgeSession, error) {
	return nil, nil
}

// noopDefaultInterfaceMonitor reports no default interface. Callers in
// route.NetworkManager tolerate a nil result: DefaultNetworkInterface returns
// nil, dispatchInterfaceUpdateLocked pauses and logs instead of dialing.
type noopDefaultInterfaceMonitor struct{}

var _ tun.DefaultInterfaceMonitor = (*noopDefaultInterfaceMonitor)(nil)

func (noopDefaultInterfaceMonitor) Start() error { return nil }

func (noopDefaultInterfaceMonitor) Close() error { return nil }

func (noopDefaultInterfaceMonitor) DefaultInterface() *control.Interface { return nil }

func (noopDefaultInterfaceMonitor) OverrideAndroidVPN() bool { return false }

func (noopDefaultInterfaceMonitor) AndroidVPNEnabled() bool { return false }

func (noopDefaultInterfaceMonitor) RegisterCallback(tun.DefaultInterfaceUpdateCallback) *list.Element[tun.DefaultInterfaceUpdateCallback] {
	return nil
}

func (noopDefaultInterfaceMonitor) UnregisterCallback(*list.Element[tun.DefaultInterfaceUpdateCallback]) {
}

func (noopDefaultInterfaceMonitor) RegisterMyInterface(string) {}

func (noopDefaultInterfaceMonitor) MyInterfaces() []string { return nil }
