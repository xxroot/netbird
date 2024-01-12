package internal

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	log "github.com/sirupsen/logrus"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"google.golang.org/grpc/codes"
	gstatus "google.golang.org/grpc/status"

	"github.com/netbirdio/netbird/client/internal/dns"
	"github.com/netbirdio/netbird/client/internal/listener"
	"github.com/netbirdio/netbird/client/internal/peer"
	"github.com/netbirdio/netbird/client/internal/stdnet"
	"github.com/netbirdio/netbird/client/ssh"
	"github.com/netbirdio/netbird/client/system"
	"github.com/netbirdio/netbird/iface"
	mgm "github.com/netbirdio/netbird/management/client"
	mgmProto "github.com/netbirdio/netbird/management/proto"
	signal "github.com/netbirdio/netbird/signal/client"
	"github.com/netbirdio/netbird/version"
)

// RunClient with main logic.
func RunClient(ctx context.Context, config *Config, statusRecorder *peer.Status) error {
	return runClient(ctx, config, statusRecorder, MobileDependency{})
}

// RunClientMobile with main logic on mobile system
func RunClientMobile(ctx context.Context, config *Config, statusRecorder *peer.Status, tunAdapter iface.TunAdapter, iFaceDiscover stdnet.ExternalIFaceDiscover, networkChangeListener listener.NetworkChangeListener, dnsAddresses []string, dnsReadyListener dns.ReadyListener) error {
	// in case of non Android os these variables will be nil
	mobileDependency := MobileDependency{
		TunAdapter:            tunAdapter,
		IFaceDiscover:         iFaceDiscover,
		NetworkChangeListener: networkChangeListener,
		HostDNSAddresses:      dnsAddresses,
		DnsReadyListener:      dnsReadyListener,
	}
	return runClient(ctx, config, statusRecorder, mobileDependency)
}

func RunClientiOS(ctx context.Context, config *Config, statusRecorder *peer.Status, fileDescriptor int32, networkChangeListener listener.NetworkChangeListener, dnsManager dns.IosDnsManager) error {
	mobileDependency := MobileDependency{
		FileDescriptor:        fileDescriptor,
		NetworkChangeListener: networkChangeListener,
		DnsManager:            dnsManager,
	}
	return runClient(ctx, config, statusRecorder, mobileDependency)
}

func runClient(ctx context.Context, config *Config, statusRecorder *peer.Status, mobileDependency MobileDependency) error {
	log.Infof("starting NetBird client version %s", version.NetbirdVersion())

	backOff := &backoff.ExponentialBackOff{
		InitialInterval:     time.Second,
		RandomizationFactor: 1,
		Multiplier:          1.7,
		MaxInterval:         15 * time.Second,
		MaxElapsedTime:      3 * 30 * 24 * time.Hour, // 3 months
		Stop:                backoff.Stop,
		Clock:               backoff.SystemClock,
	}

	state := CtxGetState(ctx)
	defer func() {
		s, err := state.Status()
		if err != nil || s != StatusNeedsLogin {
			state.Set(StatusIdle)
		}
	}()

	wrapErr := state.Wrap
	myPrivateKey, err := wgtypes.ParseKey(config.PrivateKey)
	if err != nil {
		log.Errorf("failed parsing Wireguard key %s: [%s]", config.PrivateKey, err.Error())
		return wrapErr(err)
	}

	var mgmTlsEnabled bool
	if config.ManagementURL.Scheme == "https" {
		mgmTlsEnabled = true
	}

	publicSSHKey, err := ssh.GeneratePublicKey([]byte(config.SSHKey))
	if err != nil {
		return err
	}

	defer statusRecorder.ClientStop()
	operation := func() error {
		// if context cancelled we not start new backoff cycle
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		state.Set(StatusConnecting)

		engineCtx, cancel := context.WithCancel(ctx)
		defer func() {
			statusRecorder.MarkManagementDisconnected()
			statusRecorder.CleanLocalPeerState()
			cancel()
		}()

		log.Debugf("connecting to the Management service %s", config.ManagementURL.Host)
		mgmClient, err := mgm.NewClient(engineCtx, config.ManagementURL.Host, myPrivateKey, mgmTlsEnabled)
		if err != nil {
			return wrapErr(gstatus.Errorf(codes.FailedPrecondition, "failed connecting to Management Service : %s", err))
		}
		mgmNotifier := statusRecorderToMgmConnStateNotifier(statusRecorder)
		mgmClient.SetConnStateListener(mgmNotifier)

		log.Debugf("connected to the Management service %s", config.ManagementURL.Host)
		defer func() {
			err = mgmClient.Close()
			if err != nil {
				log.Warnf("failed to close the Management service client %v", err)
			}
		}()

		// connect (just a connection, no stream yet) and login to Management Service to get an initial global Wiretrustee config
		loginResp, err := loginToManagement(engineCtx, mgmClient, publicSSHKey)
		if err != nil {
			log.Debug(err)
			if s, ok := gstatus.FromError(err); ok && (s.Code() == codes.PermissionDenied) {
				state.Set(StatusNeedsLogin)
				return backoff.Permanent(wrapErr(err)) // unrecoverable error
			}
			return wrapErr(err)
		}
		statusRecorder.MarkManagementConnected()

		localPeerState := peer.LocalPeerState{
			IP:              loginResp.GetPeerConfig().GetAddress(),
			PubKey:          myPrivateKey.PublicKey().String(),
			KernelInterface: iface.WireGuardModuleIsLoaded(),
			FQDN:            loginResp.GetPeerConfig().GetFqdn(),
		}

		statusRecorder.UpdateLocalPeerState(localPeerState)

		signalURL := fmt.Sprintf("%s://%s",
			strings.ToLower(loginResp.GetWiretrusteeConfig().GetSignal().GetProtocol().String()),
			loginResp.GetWiretrusteeConfig().GetSignal().GetUri(),
		)

		statusRecorder.UpdateSignalAddress(signalURL)

		statusRecorder.MarkSignalDisconnected()
		defer statusRecorder.MarkSignalDisconnected()

		// with the global Wiretrustee config in hand connect (just a connection, no stream yet) Signal
		signalClient, err := connectToSignal(engineCtx, loginResp.GetWiretrusteeConfig(), myPrivateKey)
		if err != nil {
			log.Error(err)
			return wrapErr(err)
		}
		defer func() {
			err = signalClient.Close()
			if err != nil {
				log.Warnf("failed closing Signal service client %v", err)
			}
		}()

		signalNotifier := statusRecorderToSignalConnStateNotifier(statusRecorder)
		signalClient.SetConnStateListener(signalNotifier)

		statusRecorder.MarkSignalConnected()

		peerConfig := loginResp.GetPeerConfig()

		engineConfig, err := createEngineConfig(myPrivateKey, config, peerConfig)
		if err != nil {
			log.Error(err)
			return wrapErr(err)
		}

		engine := NewEngine(engineCtx, cancel, signalClient, mgmClient, engineConfig, mobileDependency, statusRecorder)
		err = engine.Start()
		if err != nil {
			log.Errorf("error while starting Netbird Connection Engine: %s", err)
			return wrapErr(err)
		}

		log.Print("Netbird engine started, my IP is: ", peerConfig.Address)
		state.Set(StatusConnected)

		<-engineCtx.Done()
		statusRecorder.ClientTeardown()

		backOff.Reset()

		err = engine.Stop()
		if err != nil {
			log.Errorf("failed stopping engine %v", err)
			return wrapErr(err)
		}

		log.Info("stopped NetBird client")

		if _, err := state.Status(); err == ErrResetConnection {
			return err
		}

		return nil
	}

	statusRecorder.ClientStart()
	err = backoff.Retry(operation, backOff)
	if err != nil {
		log.Debugf("exiting client retry loop due to unrecoverable error: %s", err)
		if s, ok := gstatus.FromError(err); ok && (s.Code() == codes.PermissionDenied) {
			state.Set(StatusNeedsLogin)
		}
		return err
	}
	return nil
}

// createEngineConfig converts configuration received from Management Service to EngineConfig
func createEngineConfig(key wgtypes.Key, config *Config, peerConfig *mgmProto.PeerConfig) (*EngineConfig, error) {
	engineConf := &EngineConfig{
		WgIfaceName:          config.WgIface,
		WgAddr:               peerConfig.Address,
		IFaceBlackList:       config.IFaceBlackList,
		DisableIPv6Discovery: config.DisableIPv6Discovery,
		WgPrivateKey:         key,
		WgPort:               config.WgPort,
		SSHKey:               []byte(config.SSHKey),
		NATExternalIPs:       config.NATExternalIPs,
		CustomDNSAddress:     config.CustomDNSAddress,
		RosenpassEnabled:     config.RosenpassEnabled,
	}

	if config.PreSharedKey != "" {
		preSharedKey, err := wgtypes.ParseKey(config.PreSharedKey)
		if err != nil {
			return nil, err
		}
		engineConf.PreSharedKey = &preSharedKey
	}

	return engineConf, nil
}

// connectToSignal creates Signal Service client and established a connection
func connectToSignal(ctx context.Context, wtConfig *mgmProto.WiretrusteeConfig, ourPrivateKey wgtypes.Key) (*signal.GrpcClient, error) {
	var sigTLSEnabled bool
	if wtConfig.Signal.Protocol == mgmProto.HostConfig_HTTPS {
		sigTLSEnabled = true
	} else {
		sigTLSEnabled = false
	}

	signalClient, err := signal.NewClient(ctx, wtConfig.Signal.Uri, ourPrivateKey, sigTLSEnabled)
	if err != nil {
		log.Errorf("error while connecting to the Signal Exchange Service %s: %s", wtConfig.Signal.Uri, err)
		return nil, gstatus.Errorf(codes.FailedPrecondition, "failed connecting to Signal Service : %s", err)
	}

	return signalClient, nil
}

// loginToManagement creates Management Services client, establishes a connection, logs-in and gets a global Wiretrustee config (signal, turn, stun hosts, etc)
func loginToManagement(ctx context.Context, client mgm.Client, pubSSHKey []byte) (*mgmProto.LoginResponse, error) {

	serverPublicKey, err := client.GetServerPublicKey()
	if err != nil {
		return nil, gstatus.Errorf(codes.FailedPrecondition, "failed while getting Management Service public key: %s", err)
	}

	sysInfo := system.GetInfo(ctx)
	loginResp, err := client.Login(*serverPublicKey, sysInfo, pubSSHKey)
	if err != nil {
		return nil, err
	}

	return loginResp, nil
}

func statusRecorderToMgmConnStateNotifier(statusRecorder *peer.Status) mgm.ConnStateNotifier {
	var sri interface{} = statusRecorder
	mgmNotifier, _ := sri.(mgm.ConnStateNotifier)
	return mgmNotifier
}

func statusRecorderToSignalConnStateNotifier(statusRecorder *peer.Status) signal.ConnStateNotifier {
	var sri interface{} = statusRecorder
	notifier, _ := sri.(signal.ConnStateNotifier)
	return notifier
}
