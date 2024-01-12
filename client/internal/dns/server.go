package dns

import (
	"context"
	"fmt"
	"net/netip"
	"sync"

	"github.com/miekg/dns"
	"github.com/mitchellh/hashstructure/v2"
	log "github.com/sirupsen/logrus"

	"github.com/netbirdio/netbird/client/internal/listener"
	nbdns "github.com/netbirdio/netbird/dns"
)

// ReadyListener is a notification mechanism what indicate the server is ready to handle host dns address changes
type ReadyListener interface {
	OnReady()
}

// IosDnsManager is a dns manager interface for iOS
type IosDnsManager interface {
	ApplyDns(string)
}

// Server is a dns server interface
type Server interface {
	Initialize() error
	Stop()
	DnsIP() string
	UpdateDNSServer(serial uint64, update nbdns.Config) error
	OnUpdatedHostDNSServer(strings []string)
	SearchDomains() []string
}

type registeredHandlerMap map[string]handlerWithStop

// DefaultServer dns server object
type DefaultServer struct {
	ctx                context.Context
	ctxCancel          context.CancelFunc
	mux                sync.Mutex
	service            service
	dnsMuxMap          registeredHandlerMap
	localResolver      *localResolver
	wgInterface        WGIface
	hostManager        hostManager
	updateSerial       uint64
	previousConfigHash uint64
	currentConfig      HostDNSConfig

	// permanent related properties
	permanent        bool
	hostsDnsList     []string
	hostsDnsListLock sync.Mutex

	// make sense on mobile only
	searchDomainNotifier *notifier
	iosDnsManager        IosDnsManager
}

type handlerWithStop interface {
	dns.Handler
	stop()
}

type muxUpdate struct {
	domain  string
	handler handlerWithStop
}

// NewDefaultServer returns a new dns server
func NewDefaultServer(ctx context.Context, wgInterface WGIface, customAddress string) (*DefaultServer, error) {
	var addrPort *netip.AddrPort
	if customAddress != "" {
		parsedAddrPort, err := netip.ParseAddrPort(customAddress)
		if err != nil {
			return nil, fmt.Errorf("unable to parse the custom dns address, got error: %s", err)
		}
		addrPort = &parsedAddrPort
	}

	var dnsService service
	if wgInterface.IsUserspaceBind() {
		dnsService = newServiceViaMemory(wgInterface)
	} else {
		dnsService = newServiceViaListener(wgInterface, addrPort)
	}

	return newDefaultServer(ctx, wgInterface, dnsService), nil
}

// NewDefaultServerPermanentUpstream returns a new dns server. It optimized for mobile systems
func NewDefaultServerPermanentUpstream(ctx context.Context, wgInterface WGIface, hostsDnsList []string, config nbdns.Config, listener listener.NetworkChangeListener) *DefaultServer {
	log.Debugf("host dns address list is: %v", hostsDnsList)
	ds := newDefaultServer(ctx, wgInterface, newServiceViaMemory(wgInterface))
	ds.permanent = true
	ds.hostsDnsList = hostsDnsList
	ds.addHostRootZone()
	ds.currentConfig = dnsConfigToHostDNSConfig(config, ds.service.RuntimeIP(), ds.service.RuntimePort())
	ds.searchDomainNotifier = newNotifier(ds.SearchDomains())
	ds.searchDomainNotifier.setListener(listener)
	setServerDns(ds)
	return ds
}

// NewDefaultServerIos returns a new dns server. It optimized for ios
func NewDefaultServerIos(ctx context.Context, wgInterface WGIface, iosDnsManager IosDnsManager) *DefaultServer {
	ds := newDefaultServer(ctx, wgInterface, newServiceViaMemory(wgInterface))
	ds.iosDnsManager = iosDnsManager
	return ds
}

func newDefaultServer(ctx context.Context, wgInterface WGIface, dnsService service) *DefaultServer {
	ctx, stop := context.WithCancel(ctx)
	defaultServer := &DefaultServer{
		ctx:       ctx,
		ctxCancel: stop,
		service:   dnsService,
		dnsMuxMap: make(registeredHandlerMap),
		localResolver: &localResolver{
			registeredMap: make(registrationMap),
		},
		wgInterface: wgInterface,
	}

	return defaultServer
}

// Initialize instantiate host manager and the dns service
func (s *DefaultServer) Initialize() (err error) {
	s.mux.Lock()
	defer s.mux.Unlock()

	if s.hostManager != nil {
		return nil
	}

	if s.permanent {
		err = s.service.Listen()
		if err != nil {
			return err
		}
	}

	s.hostManager, err = s.initialize()
	return err
}

// DnsIP returns the DNS resolver server IP address
//
// When kernel space interface used it return real DNS server listener IP address
// For bind interface, fake DNS resolver address returned (second last IP address from Nebird network)
func (s *DefaultServer) DnsIP() string {
	return s.service.RuntimeIP()
}

// Stop stops the server
func (s *DefaultServer) Stop() {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.ctxCancel()

	if s.hostManager != nil {
		err := s.hostManager.restoreHostDNS()
		if err != nil {
			log.Error(err)
		}
	}

	s.service.Stop()
}

// OnUpdatedHostDNSServer update the DNS servers addresses for root zones
// It will be applied if the mgm server do not enforce DNS settings for root zone
func (s *DefaultServer) OnUpdatedHostDNSServer(hostsDnsList []string) {
	s.hostsDnsListLock.Lock()
	defer s.hostsDnsListLock.Unlock()

	s.hostsDnsList = hostsDnsList
	_, ok := s.dnsMuxMap[nbdns.RootZone]
	if ok {
		log.Debugf("on new host DNS config but skip to apply it")
		return
	}
	log.Debugf("update host DNS settings: %+v", hostsDnsList)
	s.addHostRootZone()
}

// UpdateDNSServer processes an update received from the management service
func (s *DefaultServer) UpdateDNSServer(serial uint64, update nbdns.Config) error {
	select {
	case <-s.ctx.Done():
		log.Infof("not updating DNS server as context is closed")
		return s.ctx.Err()
	default:
		if serial < s.updateSerial {
			return fmt.Errorf("not applying dns update, error: "+
				"network update is %d behind the last applied update", s.updateSerial-serial)
		}
		s.mux.Lock()
		defer s.mux.Unlock()

		if s.hostManager == nil {
			return fmt.Errorf("dns service is not initialized yet")
		}

		hash, err := hashstructure.Hash(update, hashstructure.FormatV2, &hashstructure.HashOptions{
			ZeroNil:         true,
			IgnoreZeroValue: true,
			SlicesAsSets:    true,
			UseStringer:     true,
		})
		if err != nil {
			log.Errorf("unable to hash the dns configuration update, got error: %s", err)
		}

		if s.previousConfigHash == hash {
			log.Debugf("not applying the dns configuration update as there is nothing new")
			s.updateSerial = serial
			return nil
		}

		if err := s.applyConfiguration(update); err != nil {
			return err
		}

		s.updateSerial = serial
		s.previousConfigHash = hash

		return nil
	}
}

func (s *DefaultServer) SearchDomains() []string {
	var searchDomains []string

	for _, dConf := range s.currentConfig.Domains {
		if dConf.Disabled {
			continue
		}
		if dConf.MatchOnly {
			continue
		}
		searchDomains = append(searchDomains, dConf.Domain)
	}
	return searchDomains
}

func (s *DefaultServer) applyConfiguration(update nbdns.Config) error {
	// is the service should be Disabled, we stop the listener or fake resolver
	// and proceed with a regular update to clean up the handlers and records
	if update.ServiceEnable {
		_ = s.service.Listen()
	} else if !s.permanent {
		s.service.Stop()
	}

	localMuxUpdates, localRecords, err := s.buildLocalHandlerUpdate(update.CustomZones)
	if err != nil {
		return fmt.Errorf("not applying dns update, error: %v", err)
	}
	upstreamMuxUpdates, err := s.buildUpstreamHandlerUpdate(update.NameServerGroups)
	if err != nil {
		return fmt.Errorf("not applying dns update, error: %v", err)
	}
	muxUpdates := append(localMuxUpdates, upstreamMuxUpdates...) //nolint:gocritic

	s.updateMux(muxUpdates)
	s.updateLocalResolver(localRecords)
	s.currentConfig = dnsConfigToHostDNSConfig(update, s.service.RuntimeIP(), s.service.RuntimePort())

	hostUpdate := s.currentConfig
	if s.service.RuntimePort() != defaultPort && !s.hostManager.supportCustomPort() {
		log.Warnf("the DNS manager of this peer doesn't support custom port. Disabling primary DNS setup. " +
			"Learn more at: https://docs.netbird.io/how-to/manage-dns-in-your-network#local-resolver")
		hostUpdate.RouteAll = false
	}

	if err = s.hostManager.applyDNSConfig(hostUpdate); err != nil {
		log.Error(err)
	}

	if s.searchDomainNotifier != nil {
		s.searchDomainNotifier.onNewSearchDomains(s.SearchDomains())
	}

	return nil
}

func (s *DefaultServer) buildLocalHandlerUpdate(customZones []nbdns.CustomZone) ([]muxUpdate, map[string]nbdns.SimpleRecord, error) {
	var muxUpdates []muxUpdate
	localRecords := make(map[string]nbdns.SimpleRecord, 0)

	for _, customZone := range customZones {

		if len(customZone.Records) == 0 {
			return nil, nil, fmt.Errorf("received an empty list of records")
		}

		muxUpdates = append(muxUpdates, muxUpdate{
			domain:  customZone.Domain,
			handler: s.localResolver,
		})

		for _, record := range customZone.Records {
			var class uint16 = dns.ClassINET
			if record.Class != nbdns.DefaultClass {
				return nil, nil, fmt.Errorf("received an invalid class type: %s", record.Class)
			}
			key := buildRecordKey(record.Name, class, uint16(record.Type))
			localRecords[key] = record
		}
	}
	return muxUpdates, localRecords, nil
}

func (s *DefaultServer) buildUpstreamHandlerUpdate(nameServerGroups []*nbdns.NameServerGroup) ([]muxUpdate, error) {

	var muxUpdates []muxUpdate
	for _, nsGroup := range nameServerGroups {
		if len(nsGroup.NameServers) == 0 {
			log.Warn("received a nameserver group with empty nameserver list")
			continue
		}

		handler, err := newUpstreamResolver(s.ctx, s.wgInterface.Name(), s.wgInterface.Address().IP, s.wgInterface.Address().Network)
		if err != nil {
			return nil, fmt.Errorf("unable to create a new upstream resolver, error: %v", err)
		}
		for _, ns := range nsGroup.NameServers {
			if ns.NSType != nbdns.UDPNameServerType {
				log.Warnf("skipping nameserver %s with type %s, this peer supports only %s",
					ns.IP.String(), ns.NSType.String(), nbdns.UDPNameServerType.String())
				continue
			}
			handler.upstreamServers = append(handler.upstreamServers, getNSHostPort(ns))
		}

		if len(handler.upstreamServers) == 0 {
			handler.stop()
			log.Errorf("received a nameserver group with an invalid nameserver list")
			continue
		}

		// when upstream fails to resolve domain several times over all it servers
		// it will calls this hook to exclude self from the configuration and
		// reapply DNS settings, but it not touch the original configuration and serial number
		// because it is temporal deactivation until next try
		//
		// after some period defined by upstream it tries to reactivate self by calling this hook
		// everything we need here is just to re-apply current configuration because it already
		// contains this upstream settings (temporal deactivation not removed it)
		handler.deactivate, handler.reactivate = s.upstreamCallbacks(nsGroup, handler)

		if nsGroup.Primary {
			muxUpdates = append(muxUpdates, muxUpdate{
				domain:  nbdns.RootZone,
				handler: handler,
			})
			continue
		}

		if len(nsGroup.Domains) == 0 {
			handler.stop()
			return nil, fmt.Errorf("received a non primary nameserver group with an empty domain list")
		}

		for _, domain := range nsGroup.Domains {
			if domain == "" {
				handler.stop()
				return nil, fmt.Errorf("received a nameserver group with an empty domain element")
			}
			muxUpdates = append(muxUpdates, muxUpdate{
				domain:  domain,
				handler: handler,
			})
		}
	}
	return muxUpdates, nil
}

func (s *DefaultServer) updateMux(muxUpdates []muxUpdate) {
	muxUpdateMap := make(registeredHandlerMap)

	var isContainRootUpdate bool

	for _, update := range muxUpdates {
		s.service.RegisterMux(update.domain, update.handler)
		muxUpdateMap[update.domain] = update.handler
		if existingHandler, ok := s.dnsMuxMap[update.domain]; ok {
			existingHandler.stop()
		}

		if update.domain == nbdns.RootZone {
			isContainRootUpdate = true
		}
	}

	for key, existingHandler := range s.dnsMuxMap {
		_, found := muxUpdateMap[key]
		if !found {
			if !isContainRootUpdate && key == nbdns.RootZone {
				s.hostsDnsListLock.Lock()
				s.addHostRootZone()
				s.hostsDnsListLock.Unlock()
				existingHandler.stop()
			} else {
				existingHandler.stop()
				s.service.DeregisterMux(key)
			}
		}
	}

	s.dnsMuxMap = muxUpdateMap
}

func (s *DefaultServer) updateLocalResolver(update map[string]nbdns.SimpleRecord) {
	for key := range s.localResolver.registeredMap {
		_, found := update[key]
		if !found {
			s.localResolver.deleteRecord(key)
		}
	}

	updatedMap := make(registrationMap)
	for key, record := range update {
		err := s.localResolver.registerRecord(record)
		if err != nil {
			log.Warnf("got an error while registering the record (%s), error: %v", record.String(), err)
		}
		updatedMap[key] = struct{}{}
	}

	s.localResolver.registeredMap = updatedMap
}

func getNSHostPort(ns nbdns.NameServer) string {
	return fmt.Sprintf("%s:%d", ns.IP.String(), ns.Port)
}

// upstreamCallbacks returns two functions, the first one is used to deactivate
// the upstream resolver from the configuration, the second one is used to
// reactivate it. Not allowed to call reactivate before deactivate.
func (s *DefaultServer) upstreamCallbacks(
	nsGroup *nbdns.NameServerGroup,
	handler dns.Handler,
) (deactivate func(), reactivate func()) {
	var removeIndex map[string]int
	deactivate = func() {
		s.mux.Lock()
		defer s.mux.Unlock()

		l := log.WithField("nameservers", nsGroup.NameServers)
		l.Info("temporary deactivate nameservers group due timeout")

		removeIndex = make(map[string]int)
		for _, domain := range nsGroup.Domains {
			removeIndex[domain] = -1
		}
		if nsGroup.Primary {
			removeIndex[nbdns.RootZone] = -1
			s.currentConfig.RouteAll = false
		}

		for i, item := range s.currentConfig.Domains {
			if _, found := removeIndex[item.Domain]; found {
				s.currentConfig.Domains[i].Disabled = true
				s.service.DeregisterMux(item.Domain)
				removeIndex[item.Domain] = i
			}
		}
		if err := s.hostManager.applyDNSConfig(s.currentConfig); err != nil {
			l.WithError(err).Error("fail to apply nameserver deactivation on the host")
		}
	}
	reactivate = func() {
		s.mux.Lock()
		defer s.mux.Unlock()

		for domain, i := range removeIndex {
			if i == -1 || i >= len(s.currentConfig.Domains) || s.currentConfig.Domains[i].Domain != domain {
				continue
			}
			s.currentConfig.Domains[i].Disabled = false
			s.service.RegisterMux(domain, handler)
		}

		l := log.WithField("nameservers", nsGroup.NameServers)
		l.Debug("reactivate temporary Disabled nameserver group")

		if nsGroup.Primary {
			s.currentConfig.RouteAll = true
		}
		if err := s.hostManager.applyDNSConfig(s.currentConfig); err != nil {
			l.WithError(err).Error("reactivate temporary Disabled nameserver group, DNS update apply")
		}
	}
	return
}

func (s *DefaultServer) addHostRootZone() {
	handler, err := newUpstreamResolver(s.ctx, s.wgInterface.Name(), s.wgInterface.Address().IP, s.wgInterface.Address().Network)
	if err != nil {
		log.Errorf("unable to create a new upstream resolver, error: %v", err)
		return
	}
	handler.upstreamServers = make([]string, len(s.hostsDnsList))
	for n, ua := range s.hostsDnsList {
		a, err := netip.ParseAddr(ua)
		if err != nil {
			log.Errorf("invalid upstream IP address: %s, error: %s", ua, err)
			continue
		}

		ipString := ua
		if !a.Is4() {
			ipString = fmt.Sprintf("[%s]", ua)
		}

		handler.upstreamServers[n] = fmt.Sprintf("%s:53", ipString)
	}
	handler.deactivate = func() {}
	handler.reactivate = func() {}
	s.service.RegisterMux(nbdns.RootZone, handler)
}
