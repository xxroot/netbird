package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/netbirdio/netbird/client/internal/auth"
	"github.com/netbirdio/netbird/client/system"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	gstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/netbirdio/netbird/client/internal"
	"github.com/netbirdio/netbird/client/internal/peer"
	"github.com/netbirdio/netbird/client/proto"
	"github.com/netbirdio/netbird/version"
)

// Server for service control.
type Server struct {
	rootCtx   context.Context
	actCancel context.CancelFunc

	latestConfigInput internal.ConfigInput

	logFile string

	oauthAuthFlow oauthAuthFlow

	mutex  sync.Mutex
	config *internal.Config
	proto.UnimplementedDaemonServiceServer

	statusRecorder *peer.Status
}

type oauthAuthFlow struct {
	expiresAt  time.Time
	flow       auth.OAuthFlow
	info       auth.AuthFlowInfo
	waitCancel context.CancelFunc
}

// New server instance constructor.
func New(ctx context.Context, configPath, logFile string) *Server {
	return &Server{
		rootCtx: ctx,
		latestConfigInput: internal.ConfigInput{
			ConfigPath: configPath,
		},
		logFile: logFile,
	}
}

func (s *Server) Start() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	state := internal.CtxGetState(s.rootCtx)

	// if current state contains any error, return it
	// in all other cases we can continue execution only if status is idle and up command was
	// not in the progress or already successfully established connection.
	status, err := state.Status()
	if err != nil {
		return err
	}

	if status != internal.StatusIdle {
		return nil
	}

	ctx, cancel := context.WithCancel(s.rootCtx)
	s.actCancel = cancel

	// if configuration exists, we just start connections. if is new config we skip and set status NeedsLogin
	// on failure we return error to retry
	config, err := internal.UpdateConfig(s.latestConfigInput)
	if errorStatus, ok := gstatus.FromError(err); ok && errorStatus.Code() == codes.NotFound {
		s.config, err = internal.UpdateOrCreateConfig(s.latestConfigInput)
		if err != nil {
			log.Warnf("unable to create configuration file: %v", err)
			return err
		}
		state.Set(internal.StatusNeedsLogin)
		return nil
	} else if err != nil {
		log.Warnf("unable to create configuration file: %v", err)
		return err
	}

	// if configuration exists, we just start connections.
	config, _ = internal.UpdateOldManagementURL(ctx, config, s.latestConfigInput.ConfigPath)

	s.config = config

	if s.statusRecorder == nil {
		s.statusRecorder = peer.NewRecorder(config.ManagementURL.String())
	} else {
		s.statusRecorder.UpdateManagementAddress(config.ManagementURL.String())
	}

	go func() {
		if err := internal.RunClient(ctx, config, s.statusRecorder); err != nil {
			log.Errorf("init connections: %v", err)
		}
	}()

	return nil
}

// loginAttempt attempts to login using the provided information. it returns a status in case something fails
func (s *Server) loginAttempt(ctx context.Context, setupKey, jwtToken string) (internal.StatusType, error) {
	var status internal.StatusType
	err := internal.Login(ctx, s.config, setupKey, jwtToken)
	if err != nil {
		if s, ok := gstatus.FromError(err); ok && (s.Code() == codes.InvalidArgument || s.Code() == codes.PermissionDenied) {
			log.Warnf("failed login: %v", err)
			status = internal.StatusNeedsLogin
		} else {
			log.Errorf("failed login: %v", err)
			status = internal.StatusLoginFailed
		}
		return status, err
	}
	return "", nil
}

// Login uses setup key to prepare configuration for the daemon.
func (s *Server) Login(callerCtx context.Context, msg *proto.LoginRequest) (*proto.LoginResponse, error) {
	s.mutex.Lock()
	if s.actCancel != nil {
		s.actCancel()
	}
	ctx, cancel := context.WithCancel(s.rootCtx)

	md, ok := metadata.FromIncomingContext(callerCtx)
	if ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}

	s.actCancel = cancel
	s.mutex.Unlock()

	state := internal.CtxGetState(ctx)
	defer func() {
		status, err := state.Status()
		if err != nil || (status != internal.StatusNeedsLogin && status != internal.StatusLoginFailed) {
			state.Set(internal.StatusIdle)
		}
	}()

	s.mutex.Lock()
	inputConfig := s.latestConfigInput

	if msg.ManagementUrl != "" {
		inputConfig.ManagementURL = msg.ManagementUrl
		s.latestConfigInput.ManagementURL = msg.ManagementUrl
	}

	if msg.AdminURL != "" {
		inputConfig.AdminURL = msg.AdminURL
		s.latestConfigInput.AdminURL = msg.AdminURL
	}

	if msg.CleanNATExternalIPs {
		inputConfig.NATExternalIPs = make([]string, 0)
		s.latestConfigInput.NATExternalIPs = nil
	} else if msg.NatExternalIPs != nil {
		inputConfig.NATExternalIPs = msg.NatExternalIPs
		s.latestConfigInput.NATExternalIPs = msg.NatExternalIPs
	}

	inputConfig.CustomDNSAddress = msg.CustomDNSAddress
	s.latestConfigInput.CustomDNSAddress = msg.CustomDNSAddress
	if string(msg.CustomDNSAddress) == "empty" {
		inputConfig.CustomDNSAddress = []byte{}
		s.latestConfigInput.CustomDNSAddress = []byte{}
	}

	if msg.Hostname != "" {
		// nolint
		ctx = context.WithValue(ctx, system.DeviceNameCtxKey, msg.Hostname)
	}

	if msg.RosenpassEnabled != nil {
		inputConfig.RosenpassEnabled = msg.RosenpassEnabled
		s.latestConfigInput.RosenpassEnabled = msg.RosenpassEnabled
	}

	s.mutex.Unlock()

	inputConfig.PreSharedKey = &msg.PreSharedKey

	config, err := internal.UpdateOrCreateConfig(inputConfig)
	if err != nil {
		return nil, err
	}

	if msg.ManagementUrl == "" {
		config, _ = internal.UpdateOldManagementURL(ctx, config, s.latestConfigInput.ConfigPath)
		s.config = config
		s.latestConfigInput.ManagementURL = config.ManagementURL.String()
	}

	s.mutex.Lock()
	s.config = config
	s.mutex.Unlock()

	if _, err := s.loginAttempt(ctx, "", ""); err == nil {
		state.Set(internal.StatusIdle)
		return &proto.LoginResponse{}, nil
	}

	state.Set(internal.StatusConnecting)

	if msg.SetupKey == "" {
		oAuthFlow, err := auth.NewOAuthFlow(ctx, config, msg.IsLinuxDesktopClient)
		if err != nil {
			state.Set(internal.StatusLoginFailed)
			return nil, err
		}

		if s.oauthAuthFlow.flow != nil && s.oauthAuthFlow.flow.GetClientID(ctx) == oAuthFlow.GetClientID(context.TODO()) {
			if s.oauthAuthFlow.expiresAt.After(time.Now().Add(90 * time.Second)) {
				log.Debugf("using previous oauth flow info")
				return &proto.LoginResponse{
					NeedsSSOLogin:           true,
					VerificationURI:         s.oauthAuthFlow.info.VerificationURI,
					VerificationURIComplete: s.oauthAuthFlow.info.VerificationURIComplete,
					UserCode:                s.oauthAuthFlow.info.UserCode,
				}, nil
			} else {
				log.Warnf("canceling previous waiting execution")
				if s.oauthAuthFlow.waitCancel != nil {
					s.oauthAuthFlow.waitCancel()
				}
			}
		}

		authInfo, err := oAuthFlow.RequestAuthInfo(context.TODO())
		if err != nil {
			log.Errorf("getting a request OAuth flow failed: %v", err)
			return nil, err
		}

		s.mutex.Lock()
		s.oauthAuthFlow.flow = oAuthFlow
		s.oauthAuthFlow.info = authInfo
		s.oauthAuthFlow.expiresAt = time.Now().Add(time.Duration(authInfo.ExpiresIn) * time.Second)
		s.mutex.Unlock()

		state.Set(internal.StatusNeedsLogin)

		return &proto.LoginResponse{
			NeedsSSOLogin:           true,
			VerificationURI:         authInfo.VerificationURI,
			VerificationURIComplete: authInfo.VerificationURIComplete,
			UserCode:                authInfo.UserCode,
		}, nil
	}

	if loginStatus, err := s.loginAttempt(ctx, msg.SetupKey, ""); err != nil {
		state.Set(loginStatus)
		return nil, err
	}

	return &proto.LoginResponse{}, nil
}

// WaitSSOLogin uses the userCode to validate the TokenInfo and
// waits for the user to continue with the login on a browser
func (s *Server) WaitSSOLogin(callerCtx context.Context, msg *proto.WaitSSOLoginRequest) (*proto.WaitSSOLoginResponse, error) {
	s.mutex.Lock()
	if s.actCancel != nil {
		s.actCancel()
	}
	ctx, cancel := context.WithCancel(s.rootCtx)

	md, ok := metadata.FromIncomingContext(callerCtx)
	if ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}

	if msg.Hostname != "" {
		// nolint
		ctx = context.WithValue(ctx, system.DeviceNameCtxKey, msg.Hostname)
	}

	s.actCancel = cancel
	s.mutex.Unlock()

	if s.oauthAuthFlow.flow == nil {
		return nil, gstatus.Errorf(codes.Internal, "oauth flow is not initialized")
	}

	state := internal.CtxGetState(ctx)
	defer func() {
		s, err := state.Status()
		if err != nil || (s != internal.StatusNeedsLogin && s != internal.StatusLoginFailed) {
			state.Set(internal.StatusIdle)
		}
	}()

	state.Set(internal.StatusConnecting)

	s.mutex.Lock()
	flowInfo := s.oauthAuthFlow.info
	s.mutex.Unlock()

	if flowInfo.UserCode != msg.UserCode {
		state.Set(internal.StatusLoginFailed)
		return nil, gstatus.Errorf(codes.InvalidArgument, "sso user code is invalid")
	}

	if s.oauthAuthFlow.waitCancel != nil {
		s.oauthAuthFlow.waitCancel()
	}

	waitTimeout := time.Until(s.oauthAuthFlow.expiresAt)
	waitCTX, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()

	s.mutex.Lock()
	s.oauthAuthFlow.waitCancel = cancel
	s.mutex.Unlock()

	tokenInfo, err := s.oauthAuthFlow.flow.WaitToken(waitCTX, flowInfo)
	if err != nil {
		if err == context.Canceled {
			return nil, nil //nolint:nilnil
		}
		s.mutex.Lock()
		s.oauthAuthFlow.expiresAt = time.Now()
		s.mutex.Unlock()
		state.Set(internal.StatusLoginFailed)
		log.Errorf("waiting for browser login failed: %v", err)
		return nil, err
	}

	s.mutex.Lock()
	s.oauthAuthFlow.expiresAt = time.Now()
	s.mutex.Unlock()

	if loginStatus, err := s.loginAttempt(ctx, "", tokenInfo.GetTokenToUse()); err != nil {
		state.Set(loginStatus)
		return nil, err
	}

	return &proto.WaitSSOLoginResponse{}, nil
}

// Up starts engine work in the daemon.
func (s *Server) Up(callerCtx context.Context, _ *proto.UpRequest) (*proto.UpResponse, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	state := internal.CtxGetState(s.rootCtx)

	// if current state contains any error, return it
	// in all other cases we can continue execution only if status is idle and up command was
	// not in the progress or already successfully established connection.
	status, err := state.Status()
	if err != nil {
		return nil, err
	}
	if status != internal.StatusIdle {
		return nil, fmt.Errorf("up already in progress: current status %s", status)
	}

	// it should be nil here, but .
	if s.actCancel != nil {
		s.actCancel()
	}
	ctx, cancel := context.WithCancel(s.rootCtx)

	md, ok := metadata.FromIncomingContext(callerCtx)
	if ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}

	s.actCancel = cancel

	if s.config == nil {
		return nil, fmt.Errorf("config is not defined, please call login command first")
	}

	if s.statusRecorder == nil {
		s.statusRecorder = peer.NewRecorder(s.config.ManagementURL.String())
	} else {
		s.statusRecorder.UpdateManagementAddress(s.config.ManagementURL.String())
	}

	go func() {
		if err := internal.RunClient(ctx, s.config, s.statusRecorder); err != nil {
			log.Errorf("run client connection: %v", err)
			return
		}
	}()

	return &proto.UpResponse{}, nil
}

// Down engine work in the daemon.
func (s *Server) Down(_ context.Context, _ *proto.DownRequest) (*proto.DownResponse, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.actCancel == nil {
		return nil, fmt.Errorf("service is not up")
	}
	s.actCancel()
	state := internal.CtxGetState(s.rootCtx)
	state.Set(internal.StatusIdle)

	return &proto.DownResponse{}, nil
}

// Status starts engine work in the daemon.
func (s *Server) Status(
	_ context.Context,
	msg *proto.StatusRequest,
) (*proto.StatusResponse, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	status, err := internal.CtxGetState(s.rootCtx).Status()
	if err != nil {
		return nil, err
	}

	statusResponse := proto.StatusResponse{Status: string(status), DaemonVersion: version.NetbirdVersion()}

	if s.statusRecorder == nil {
		s.statusRecorder = peer.NewRecorder(s.config.ManagementURL.String())
	} else {
		s.statusRecorder.UpdateManagementAddress(s.config.ManagementURL.String())
	}

	if msg.GetFullPeerStatus {
		fullStatus := s.statusRecorder.GetFullStatus()
		pbFullStatus := toProtoFullStatus(fullStatus)
		statusResponse.FullStatus = pbFullStatus
	}

	return &statusResponse, nil
}

// GetConfig of the daemon.
func (s *Server) GetConfig(_ context.Context, _ *proto.GetConfigRequest) (*proto.GetConfigResponse, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	managementURL := s.latestConfigInput.ManagementURL
	adminURL := s.latestConfigInput.AdminURL
	preSharedKey := ""

	if s.config != nil {
		if managementURL == "" && s.config.ManagementURL != nil {
			managementURL = s.config.ManagementURL.String()
		}

		if s.config.AdminURL != nil {
			adminURL = s.config.AdminURL.String()
		}

		preSharedKey = s.config.PreSharedKey
		if preSharedKey != "" {
			preSharedKey = "**********"
		}

	}

	return &proto.GetConfigResponse{
		ManagementUrl: managementURL,
		AdminURL:      adminURL,
		ConfigFile:    s.latestConfigInput.ConfigPath,
		LogFile:       s.logFile,
		PreSharedKey:  preSharedKey,
	}, nil
}

func toProtoFullStatus(fullStatus peer.FullStatus) *proto.FullStatus {
	pbFullStatus := proto.FullStatus{
		ManagementState: &proto.ManagementState{},
		SignalState:     &proto.SignalState{},
		LocalPeerState:  &proto.LocalPeerState{},
		Peers:           []*proto.PeerState{},
	}

	pbFullStatus.ManagementState.URL = fullStatus.ManagementState.URL
	pbFullStatus.ManagementState.Connected = fullStatus.ManagementState.Connected

	pbFullStatus.SignalState.URL = fullStatus.SignalState.URL
	pbFullStatus.SignalState.Connected = fullStatus.SignalState.Connected

	pbFullStatus.LocalPeerState.IP = fullStatus.LocalPeerState.IP
	pbFullStatus.LocalPeerState.PubKey = fullStatus.LocalPeerState.PubKey
	pbFullStatus.LocalPeerState.KernelInterface = fullStatus.LocalPeerState.KernelInterface
	pbFullStatus.LocalPeerState.Fqdn = fullStatus.LocalPeerState.FQDN

	for _, peerState := range fullStatus.Peers {
		pbPeerState := &proto.PeerState{
			IP:                     peerState.IP,
			PubKey:                 peerState.PubKey,
			ConnStatus:             peerState.ConnStatus.String(),
			ConnStatusUpdate:       timestamppb.New(peerState.ConnStatusUpdate),
			Relayed:                peerState.Relayed,
			Direct:                 peerState.Direct,
			LocalIceCandidateType:  peerState.LocalIceCandidateType,
			RemoteIceCandidateType: peerState.RemoteIceCandidateType,
			Fqdn:                   peerState.FQDN,
		}
		pbFullStatus.Peers = append(pbFullStatus.Peers, pbPeerState)
	}
	return &pbFullStatus
}
