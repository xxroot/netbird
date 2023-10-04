package server

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/rs/xid"

	"github.com/netbirdio/netbird/management/server/activity"
	"github.com/netbirdio/netbird/management/server/status"

	log "github.com/sirupsen/logrus"

	"github.com/netbirdio/netbird/management/proto"
)

// PeerSystemMeta is a metadata of a Peer machine system
type PeerSystemMeta struct {
	Hostname  string
	GoOS      string
	Kernel    string
	Core      string
	Platform  string
	OS        string
	WtVersion string
	UIVersion string
}

func (p PeerSystemMeta) isEqual(other PeerSystemMeta) bool {
	return p.Hostname == other.Hostname &&
		p.GoOS == other.GoOS &&
		p.Kernel == other.Kernel &&
		p.Core == other.Core &&
		p.Platform == other.Platform &&
		p.OS == other.OS &&
		p.WtVersion == other.WtVersion &&
		p.UIVersion == other.UIVersion
}

type PeerStatus struct {
	// LastSeen is the last time peer was connected to the management service
	LastSeen time.Time
	// Connected indicates whether peer is connected to the management service or not
	Connected bool
	// LoginExpired
	LoginExpired bool
}

// PeerSync used as a data object between the gRPC API and AccountManager on Sync request.
type PeerSync struct {
	// WireGuardPubKey is a peers WireGuard public key
	WireGuardPubKey string
}

// PeerLogin used as a data object between the gRPC API and AccountManager on Login request.
type PeerLogin struct {
	// WireGuardPubKey is a peers WireGuard public key
	WireGuardPubKey string
	// SSHKey is a peer's ssh key. Can be empty (e.g., old version do not provide it, or this feature is disabled)
	SSHKey string
	// Meta is the system information passed by peer, must be always present.
	Meta PeerSystemMeta
	// UserID indicates that JWT was used to log in, and it was valid. Can be empty when SetupKey is used or auth is not required.
	UserID string
	// SetupKey references to a server.SetupKey to log in. Can be empty when UserID is used or auth is not required.
	SetupKey string
}

// Peer represents a machine connected to the network.
// The Peer is a WireGuard peer identified by a public key
type Peer struct {
	// ID is an internal ID of the peer
	ID string `gorm:"primaryKey"`
	// AccountID is a reference to Account that this object belongs
	AccountID string `json:"-" gorm:"index;uniqueIndex:idx_peers_account_id_ip"`
	// WireGuard public key
	Key string `gorm:"index"`
	// A setup key this peer was registered with
	SetupKey string
	// IP address of the Peer
	IP net.IP `gorm:"uniqueIndex:idx_peers_account_id_ip"`
	// Meta is a Peer system meta data
	Meta PeerSystemMeta `gorm:"embedded;embeddedPrefix:meta_"`
	// Name is peer's name (machine name)
	Name string
	// DNSLabel is the parsed peer name for domain resolution. It is used to form an FQDN by appending the account's
	// domain to the peer label. e.g. peer-dns-label.netbird.cloud
	DNSLabel string
	// Status peer's management connection status
	Status *PeerStatus `gorm:"embedded;embeddedPrefix:peer_status_"`
	// The user ID that registered the peer
	UserID string
	// SSHKey is a public SSH key of the peer
	SSHKey string
	// SSHEnabled indicates whether SSH server is enabled on the peer
	SSHEnabled bool
	// LoginExpirationEnabled indicates whether peer's login expiration is enabled and once expired the peer has to re-login.
	// Works with LastLogin
	LoginExpirationEnabled bool
	// LastLogin the time when peer performed last login operation
	LastLogin time.Time
	// Indicate ephemeral peer attribute
	Ephemeral bool
}

// AddedWithSSOLogin indicates whether this peer has been added with an SSO login by a user.
func (p *Peer) AddedWithSSOLogin() bool {
	return p.UserID != ""
}

// Copy copies Peer object
func (p *Peer) Copy() *Peer {
	peerStatus := p.Status
	if peerStatus != nil {
		peerStatus = p.Status.Copy()
	}
	return &Peer{
		ID:                     p.ID,
		AccountID:              p.AccountID,
		Key:                    p.Key,
		SetupKey:               p.SetupKey,
		IP:                     p.IP,
		Meta:                   p.Meta,
		Name:                   p.Name,
		DNSLabel:               p.DNSLabel,
		Status:                 peerStatus,
		UserID:                 p.UserID,
		SSHKey:                 p.SSHKey,
		SSHEnabled:             p.SSHEnabled,
		LoginExpirationEnabled: p.LoginExpirationEnabled,
		LastLogin:              p.LastLogin,
		Ephemeral:              p.Ephemeral,
	}
}

// UpdateMetaIfNew updates peer's system metadata if new information is provided
// returns true if meta was updated, false otherwise
func (p *Peer) UpdateMetaIfNew(meta PeerSystemMeta) bool {
	// Avoid overwriting UIVersion if the update was triggered sole by the CLI client
	if meta.UIVersion == "" {
		meta.UIVersion = p.Meta.UIVersion
	}

	if p.Meta.isEqual(meta) {
		return false
	}
	p.Meta = meta
	return true
}

// MarkLoginExpired marks peer's status expired or not
func (p *Peer) MarkLoginExpired(expired bool) {
	newStatus := p.Status.Copy()
	newStatus.LoginExpired = expired
	if expired {
		newStatus.Connected = false
	}
	p.Status = newStatus
}

// LoginExpired indicates whether the peer's login has expired or not.
// If Peer.LastLogin plus the expiresIn duration has happened already; then login has expired.
// Return true if a login has expired, false otherwise, and time left to expiration (negative when expired).
// Login expiration can be disabled/enabled on a Peer level via Peer.LoginExpirationEnabled property.
// Login expiration can also be disabled/enabled globally on the Account level via Settings.PeerLoginExpirationEnabled.
// Only peers added by interactive SSO login can be expired.
func (p *Peer) LoginExpired(expiresIn time.Duration) (bool, time.Duration) {
	if !p.AddedWithSSOLogin() || !p.LoginExpirationEnabled {
		return false, 0
	}
	expiresAt := p.LastLogin.Add(expiresIn)
	now := time.Now()
	timeLeft := expiresAt.Sub(now)
	return timeLeft <= 0, timeLeft
}

// FQDN returns peers FQDN combined of the peer's DNS label and the system's DNS domain
func (p *Peer) FQDN(dnsDomain string) string {
	if dnsDomain == "" {
		return ""
	}
	return fmt.Sprintf("%s.%s", p.DNSLabel, dnsDomain)
}

// EventMeta returns activity event meta related to the peer
func (p *Peer) EventMeta(dnsDomain string) map[string]any {
	return map[string]any{"name": p.Name, "fqdn": p.FQDN(dnsDomain), "ip": p.IP}
}

// Copy PeerStatus
func (p *PeerStatus) Copy() *PeerStatus {
	return &PeerStatus{
		LastSeen:     p.LastSeen,
		Connected:    p.Connected,
		LoginExpired: p.LoginExpired,
	}
}

// GetPeers returns a list of peers under the given account filtering out peers that do not belong to a user if
// the current user is not an admin.
func (am *DefaultAccountManager) GetPeers(accountID, userID string) ([]*Peer, error) {
	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return nil, err
	}

	user, err := account.FindUser(userID)
	if err != nil {
		return nil, err
	}

	peers := make([]*Peer, 0)
	peersMap := make(map[string]*Peer)
	for _, peer := range account.Peers {
		if !user.IsAdmin() && user.Id != peer.UserID {
			// only display peers that belong to the current user if the current user is not an admin
			continue
		}
		p := peer.Copy()
		peers = append(peers, p)
		peersMap[peer.ID] = p
	}

	// fetch all the peers that have access to the user's peers
	for _, peer := range peers {
		aclPeers, _ := account.getPeerConnectionResources(peer.ID)
		for _, p := range aclPeers {
			peersMap[p.ID] = p
		}
	}

	peers = make([]*Peer, 0, len(peersMap))
	for _, peer := range peersMap {
		peers = append(peers, peer)
	}

	return peers, nil
}

// MarkPeerConnected marks peer as connected (true) or disconnected (false)
func (am *DefaultAccountManager) MarkPeerConnected(peerPubKey string, connected bool) error {
	account, err := am.Store.GetAccountByPeerPubKey(peerPubKey)
	if err != nil {
		return err
	}

	unlock := am.Store.AcquireAccountLock(account.Id)
	defer unlock()

	// ensure that we consider modification happened meanwhile (because we were outside the account lock when we fetched the account)
	account, err = am.Store.GetAccount(account.Id)
	if err != nil {
		return err
	}

	peer, err := account.FindPeerByPubKey(peerPubKey)
	if err != nil {
		return err
	}

	oldStatus := peer.Status.Copy()
	newStatus := oldStatus
	newStatus.LastSeen = time.Now().UTC()
	newStatus.Connected = connected
	// whenever peer got connected that means that it logged in successfully
	if newStatus.Connected {
		newStatus.LoginExpired = false
	}
	peer.Status = newStatus
	account.UpdatePeer(peer)

	err = am.Store.SavePeerStatus(account.Id, peer.ID, *newStatus)
	if err != nil {
		return err
	}

	if peer.AddedWithSSOLogin() && peer.LoginExpirationEnabled && account.Settings.PeerLoginExpirationEnabled {
		am.checkAndSchedulePeerLoginExpiration(account)
	}

	if oldStatus.LoginExpired {
		// we need to update other peers because when peer login expires all other peers are notified to disconnect from
		// the expired one. Here we notify them that connection is now allowed again.
		am.updateAccountPeers(account)
	}

	return nil
}

// UpdatePeer updates peer. Only Peer.Name, Peer.SSHEnabled, and Peer.LoginExpirationEnabled can be updated.
func (am *DefaultAccountManager) UpdatePeer(accountID, userID string, update *Peer) (*Peer, error) {
	unlock := am.Store.AcquireAccountLock(accountID)
	defer unlock()

	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return nil, err
	}

	peer := account.GetPeer(update.ID)
	if peer == nil {
		return nil, status.Errorf(status.NotFound, "peer %s not found", update.ID)
	}

	if peer.SSHEnabled != update.SSHEnabled {
		peer.SSHEnabled = update.SSHEnabled
		event := activity.PeerSSHEnabled
		if !update.SSHEnabled {
			event = activity.PeerSSHDisabled
		}
		am.storeEvent(userID, peer.IP.String(), accountID, event, peer.EventMeta(am.GetDNSDomain()))
	}

	if peer.Name != update.Name {
		peer.Name = update.Name

		existingLabels := account.getPeerDNSLabels()

		newLabel, err := getPeerHostLabel(peer.Name, existingLabels)
		if err != nil {
			return nil, err
		}

		peer.DNSLabel = newLabel

		am.storeEvent(userID, peer.ID, accountID, activity.PeerRenamed, peer.EventMeta(am.GetDNSDomain()))
	}

	if peer.LoginExpirationEnabled != update.LoginExpirationEnabled {

		if !peer.AddedWithSSOLogin() {
			return nil, status.Errorf(status.PreconditionFailed, "this peer hasn't been added with the SSO login, therefore the login expiration can't be updated")
		}

		peer.LoginExpirationEnabled = update.LoginExpirationEnabled

		event := activity.PeerLoginExpirationEnabled
		if !update.LoginExpirationEnabled {
			event = activity.PeerLoginExpirationDisabled
		}
		am.storeEvent(userID, peer.IP.String(), accountID, event, peer.EventMeta(am.GetDNSDomain()))

		if peer.AddedWithSSOLogin() && peer.LoginExpirationEnabled && account.Settings.PeerLoginExpirationEnabled {
			am.checkAndSchedulePeerLoginExpiration(account)
		}
	}

	account.UpdatePeer(peer)

	err = am.Store.SaveAccount(account)
	if err != nil {
		return nil, err
	}

	am.updateAccountPeers(account)

	return peer, nil
}

// deletePeers will delete all specified peers and send updates to the remote peers. Don't call without acquiring account lock
func (am *DefaultAccountManager) deletePeers(account *Account, peerIDs []string, userID string) error {

	// the first loop is needed to ensure all peers present under the account before modifying, otherwise
	// we might have some inconsistencies
	peers := make([]*Peer, 0, len(peerIDs))
	for _, peerID := range peerIDs {

		peer := account.GetPeer(peerID)
		if peer == nil {
			return status.Errorf(status.NotFound, "peer %s not found", peerID)
		}
		peers = append(peers, peer)
	}

	// the 2nd loop performs the actual modification
	for _, peer := range peers {
		account.DeletePeer(peer.ID)
		am.peersUpdateManager.SendUpdate(peer.ID,
			&UpdateMessage{
				Update: &proto.SyncResponse{
					// fill those field for backward compatibility
					RemotePeers:        []*proto.RemotePeerConfig{},
					RemotePeersIsEmpty: true,
					// new field
					NetworkMap: &proto.NetworkMap{
						Serial:               account.Network.CurrentSerial(),
						RemotePeers:          []*proto.RemotePeerConfig{},
						RemotePeersIsEmpty:   true,
						FirewallRules:        []*proto.FirewallRule{},
						FirewallRulesIsEmpty: true,
					},
				},
			})
		am.peersUpdateManager.CloseChannel(peer.ID)
		am.storeEvent(userID, peer.ID, account.Id, activity.PeerRemovedByUser, peer.EventMeta(am.GetDNSDomain()))
	}

	return nil
}

// DeletePeer removes peer from the account by its IP
func (am *DefaultAccountManager) DeletePeer(accountID, peerID, userID string) error {
	unlock := am.Store.AcquireAccountLock(accountID)
	defer unlock()

	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return err
	}

	err = am.deletePeers(account, []string{peerID}, userID)
	if err != nil {
		return err
	}

	err = am.Store.SaveAccount(account)
	if err != nil {
		return err
	}

	am.updateAccountPeers(account)

	return nil
}

// GetNetworkMap returns Network map for a given peer (omits original peer from the Peers result)
func (am *DefaultAccountManager) GetNetworkMap(peerID string) (*NetworkMap, error) {
	account, err := am.Store.GetAccountByPeerID(peerID)
	if err != nil {
		return nil, err
	}

	peer := account.GetPeer(peerID)
	if peer == nil {
		return nil, status.Errorf(status.NotFound, "peer with ID %s not found", peerID)
	}
	return account.GetPeerNetworkMap(peer.ID, am.dnsDomain), nil
}

// GetPeerNetwork returns the Network for a given peer
func (am *DefaultAccountManager) GetPeerNetwork(peerID string) (*Network, error) {
	account, err := am.Store.GetAccountByPeerID(peerID)
	if err != nil {
		return nil, err
	}

	return account.Network.Copy(), err
}

// AddPeer adds a new peer to the Store.
// Each Account has a list of pre-authorized SetupKey and if no Account has a given key err with a code status.PermissionDenied
// will be returned, meaning the setup key is invalid or not found.
// If a User ID is provided, it means that we passed the authentication using JWT, then we look for account by User ID and register the peer
// to it. We also add the User ID to the peer metadata to identify registrant. If no userID provided, then fail with status.PermissionDenied
// Each new Peer will be assigned a new next net.IP from the Account.Network and Account.Network.LastIP will be updated (IP's are not reused).
// The peer property is just a placeholder for the Peer properties to pass further
func (am *DefaultAccountManager) AddPeer(setupKey, userID string, peer *Peer) (*Peer, *NetworkMap, error) {
	if setupKey == "" && userID == "" {
		// no auth method provided => reject access
		return nil, nil, status.Errorf(status.Unauthenticated, "no peer auth method provided, please use a setup key or interactive SSO login")
	}

	upperKey := strings.ToUpper(setupKey)
	var account *Account
	var err error
	addedByUser := false
	if len(userID) > 0 {
		addedByUser = true
		account, err = am.Store.GetAccountByUser(userID)
	} else {
		account, err = am.Store.GetAccountBySetupKey(setupKey)
	}
	if err != nil {
		return nil, nil, status.Errorf(status.NotFound, "failed adding new peer: account not found")
	}

	unlock := am.Store.AcquireAccountLock(account.Id)
	defer unlock()

	// ensure that we consider modification happened meanwhile (because we were outside the account lock when we fetched the account)
	account, err = am.Store.GetAccount(account.Id)
	if err != nil {
		return nil, nil, err
	}

	// This is a handling for the case when the same machine (with the same WireGuard pub key) tries to register twice.
	// Such case is possible when AddPeer function takes long time to finish after AcquireAccountLock (e.g., database is slow)
	// and the peer disconnects with a timeout and tries to register again.
	// We just check if this machine has been registered before and reject the second registration.
	// The connecting peer should be able to recover with a retry.
	_, err = account.FindPeerByPubKey(peer.Key)
	if err == nil {
		return nil, nil, status.Errorf(status.PreconditionFailed, "peer has been already registered")
	}

	opEvent := &activity.Event{
		Timestamp: time.Now().UTC(),
		AccountID: account.Id,
	}

	var ephemeral bool
	if !addedByUser {
		// validate the setup key if adding with a key
		sk, err := account.FindSetupKey(upperKey)
		if err != nil {
			return nil, nil, err
		}

		if !sk.IsValid() {
			return nil, nil, status.Errorf(status.PreconditionFailed, "couldn't add peer: setup key is invalid")
		}

		account.SetupKeys[sk.Key] = sk.IncrementUsage()
		opEvent.InitiatorID = sk.Id
		opEvent.Activity = activity.PeerAddedWithSetupKey
		ephemeral = sk.Ephemeral
	} else {
		opEvent.InitiatorID = userID
		opEvent.Activity = activity.PeerAddedByUser
	}

	takenIps := account.getTakenIPs()
	existingLabels := account.getPeerDNSLabels()

	newLabel, err := getPeerHostLabel(peer.Meta.Hostname, existingLabels)
	if err != nil {
		return nil, nil, err
	}

	peer.DNSLabel = newLabel
	network := account.Network
	nextIp, err := AllocatePeerIP(network.Net, takenIps)
	if err != nil {
		return nil, nil, err
	}

	newPeer := &Peer{
		ID:                     xid.New().String(),
		Key:                    peer.Key,
		SetupKey:               upperKey,
		IP:                     nextIp,
		Meta:                   peer.Meta,
		Name:                   peer.Meta.Hostname,
		DNSLabel:               newLabel,
		UserID:                 userID,
		Status:                 &PeerStatus{Connected: false, LastSeen: time.Now().UTC()},
		SSHEnabled:             false,
		SSHKey:                 peer.SSHKey,
		LastLogin:              time.Now().UTC(),
		LoginExpirationEnabled: addedByUser,
		Ephemeral:              ephemeral,
	}

	// add peer to 'All' group
	group, err := account.GetGroupAll()
	if err != nil {
		return nil, nil, err
	}
	group.Peers = append(group.Peers, newPeer.ID)

	var groupsToAdd []string
	if addedByUser {
		groupsToAdd, err = account.getUserGroups(userID)
		if err != nil {
			return nil, nil, err
		}
	} else {
		groupsToAdd, err = account.getSetupKeyGroups(upperKey)
		if err != nil {
			return nil, nil, err
		}
	}

	if len(groupsToAdd) > 0 {
		for _, s := range groupsToAdd {
			if g, ok := account.Groups[s]; ok && g.Name != "All" {
				g.Peers = append(g.Peers, newPeer.ID)
			}
		}
	}

	account.Peers[newPeer.ID] = newPeer
	account.Network.IncSerial()
	err = am.Store.SaveAccount(account)
	if err != nil {
		return nil, nil, err
	}

	opEvent.TargetID = newPeer.ID
	opEvent.Meta = newPeer.EventMeta(am.GetDNSDomain())
	am.storeEvent(opEvent.InitiatorID, opEvent.TargetID, opEvent.AccountID, opEvent.Activity, opEvent.Meta)

	am.updateAccountPeers(account)

	networkMap := account.GetPeerNetworkMap(newPeer.ID, am.dnsDomain)
	return newPeer, networkMap, nil
}

// SyncPeer checks whether peer is eligible for receiving NetworkMap (authenticated) and returns its NetworkMap if eligible
func (am *DefaultAccountManager) SyncPeer(sync PeerSync) (*Peer, *NetworkMap, error) {
	account, err := am.Store.GetAccountByPeerPubKey(sync.WireGuardPubKey)
	if err != nil {
		if errStatus, ok := status.FromError(err); ok && errStatus.Type() == status.NotFound {
			return nil, nil, status.Errorf(status.Unauthenticated, "peer is not registered")
		}
		return nil, nil, err
	}

	// we found the peer, and we follow a normal login flow
	unlock := am.Store.AcquireAccountLock(account.Id)
	defer unlock()

	// fetch the account from the store once more after acquiring lock to avoid concurrent updates inconsistencies
	account, err = am.Store.GetAccount(account.Id)
	if err != nil {
		return nil, nil, err
	}

	peer, err := account.FindPeerByPubKey(sync.WireGuardPubKey)
	if err != nil {
		return nil, nil, status.Errorf(status.Unauthenticated, "peer is not registered")
	}

	err = checkIfPeerOwnerIsBlocked(peer, account)
	if err != nil {
		return nil, nil, err
	}

	if peerLoginExpired(peer, account) {
		return nil, nil, status.Errorf(status.PermissionDenied, "peer login has expired, please log in once more")
	}
	return peer, account.GetPeerNetworkMap(peer.ID, am.dnsDomain), nil
}

// LoginPeer logs in or registers a peer.
// If peer doesn't exist the function checks whether a setup key or a user is present and registers a new peer if so.
func (am *DefaultAccountManager) LoginPeer(login PeerLogin) (*Peer, *NetworkMap, error) {
	account, err := am.Store.GetAccountByPeerPubKey(login.WireGuardPubKey)
	if err != nil {
		if errStatus, ok := status.FromError(err); ok && errStatus.Type() == status.NotFound {
			// we couldn't find this peer by its public key which can mean that peer hasn't been registered yet.
			// Try registering it.
			return am.AddPeer(login.SetupKey, login.UserID, &Peer{
				Key:    login.WireGuardPubKey,
				Meta:   login.Meta,
				SSHKey: login.SSHKey,
			})
		}
		log.Errorf("failed while logging in peer %s: %v", login.WireGuardPubKey, err)
		return nil, nil, status.Errorf(status.Internal, "failed while logging in peer")
	}

	// we found the peer, and we follow a normal login flow
	unlock := am.Store.AcquireAccountLock(account.Id)
	defer unlock()

	// fetch the account from the store once more after acquiring lock to avoid concurrent updates inconsistencies
	account, err = am.Store.GetAccount(account.Id)
	if err != nil {
		return nil, nil, err
	}

	peer, err := account.FindPeerByPubKey(login.WireGuardPubKey)
	if err != nil {
		return nil, nil, status.Errorf(status.Unauthenticated, "peer is not registered")
	}

	err = checkIfPeerOwnerIsBlocked(peer, account)
	if err != nil {
		return nil, nil, err
	}

	// this flag prevents unnecessary calls to the persistent store.
	shouldStoreAccount := false
	updateRemotePeers := false
	if peerLoginExpired(peer, account) {
		err = checkAuth(login.UserID, peer)
		if err != nil {
			return nil, nil, err
		}
		// If peer was expired before and if it reached this point, it is re-authenticated.
		// UserID is present, meaning that JWT validation passed successfully in the API layer.
		updatePeerLastLogin(peer, account)
		updateRemotePeers = true
		shouldStoreAccount = true

		am.storeEvent(login.UserID, peer.ID, account.Id, activity.UserLoggedInPeer, peer.EventMeta(am.GetDNSDomain()))
	}

	peer, updated := updatePeerMeta(peer, login.Meta, account)
	if updated {
		shouldStoreAccount = true
	}

	peer, err = am.checkAndUpdatePeerSSHKey(peer, account, login.SSHKey)
	if err != nil {
		return nil, nil, err
	}

	if shouldStoreAccount {
		err = am.Store.SaveAccount(account)
		if err != nil {
			return nil, nil, err
		}
	}

	if updateRemotePeers {
		am.updateAccountPeers(account)
	}
	return peer, account.GetPeerNetworkMap(peer.ID, am.dnsDomain), nil
}

func checkIfPeerOwnerIsBlocked(peer *Peer, account *Account) error {
	if peer.AddedWithSSOLogin() {
		user, err := account.FindUser(peer.UserID)
		if err != nil {
			return status.Errorf(status.PermissionDenied, "user doesn't exist")
		}
		if user.IsBlocked() {
			return status.Errorf(status.PermissionDenied, "user is blocked")
		}
	}
	return nil
}

func checkAuth(loginUserID string, peer *Peer) error {
	if loginUserID == "" {
		// absence of a user ID indicates that JWT wasn't provided.
		return status.Errorf(status.PermissionDenied, "peer login has expired, please log in once more")
	}
	if peer.UserID != loginUserID {
		log.Warnf("user mismatch when loggin in peer %s: peer user %s, login user %s ", peer.ID, peer.UserID, loginUserID)
		return status.Errorf(status.Unauthenticated, "can't login")
	}
	return nil
}

func peerLoginExpired(peer *Peer, account *Account) bool {
	expired, expiresIn := peer.LoginExpired(account.Settings.PeerLoginExpiration)
	expired = account.Settings.PeerLoginExpirationEnabled && expired
	if expired || peer.Status.LoginExpired {
		log.Debugf("peer's %s login expired %v ago", peer.ID, expiresIn)
		return true
	}
	return false
}

func updatePeerLastLogin(peer *Peer, account *Account) {
	peer.UpdateLastLogin()
	account.UpdatePeer(peer)
}

// UpdateLastLogin and set login expired false
func (p *Peer) UpdateLastLogin() *Peer {
	p.LastLogin = time.Now().UTC()
	newStatus := p.Status.Copy()
	newStatus.LoginExpired = false
	p.Status = newStatus
	return p
}

func (am *DefaultAccountManager) checkAndUpdatePeerSSHKey(peer *Peer, account *Account, newSSHKey string) (*Peer, error) {
	if len(newSSHKey) == 0 {
		log.Debugf("no new SSH key provided for peer %s, skipping update", peer.ID)
		return peer, nil
	}

	if peer.SSHKey == newSSHKey {
		log.Debugf("same SSH key provided for peer %s, skipping update", peer.ID)
		return peer, nil
	}

	peer.SSHKey = newSSHKey
	account.UpdatePeer(peer)

	err := am.Store.SaveAccount(account)
	if err != nil {
		return nil, err
	}

	// trigger network map update
	am.updateAccountPeers(account)

	return peer, nil
}

// UpdatePeerSSHKey updates peer's public SSH key
func (am *DefaultAccountManager) UpdatePeerSSHKey(peerID string, sshKey string) error {
	if sshKey == "" {
		log.Debugf("empty SSH key provided for peer %s, skipping update", peerID)
		return nil
	}

	account, err := am.Store.GetAccountByPeerID(peerID)
	if err != nil {
		return err
	}

	unlock := am.Store.AcquireAccountLock(account.Id)
	defer unlock()

	// ensure that we consider modification happened meanwhile (because we were outside the account lock when we fetched the account)
	account, err = am.Store.GetAccount(account.Id)
	if err != nil {
		return err
	}

	peer := account.GetPeer(peerID)
	if peer == nil {
		return status.Errorf(status.NotFound, "peer with ID %s not found", peerID)
	}

	if peer.SSHKey == sshKey {
		log.Debugf("same SSH key provided for peer %s, skipping update", peerID)
		return nil
	}

	peer.SSHKey = sshKey
	account.UpdatePeer(peer)

	err = am.Store.SaveAccount(account)
	if err != nil {
		return err
	}

	// trigger network map update
	am.updateAccountPeers(account)

	return nil
}

// GetPeer for a given accountID, peerID and userID error if not found.
func (am *DefaultAccountManager) GetPeer(accountID, peerID, userID string) (*Peer, error) {
	unlock := am.Store.AcquireAccountLock(accountID)
	defer unlock()

	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return nil, err
	}

	user, err := account.FindUser(userID)
	if err != nil {
		return nil, err
	}

	peer := account.GetPeer(peerID)
	if peer == nil {
		return nil, status.Errorf(status.NotFound, "peer with %s not found under account %s", peerID, accountID)
	}

	// if admin or user owns this peer, return peer
	if user.IsAdmin() || peer.UserID == userID {
		return peer, nil
	}

	// it is also possible that user doesn't own the peer but some of his peers have access to it,
	// this is a valid case, show the peer as well.
	userPeers, err := account.FindUserPeers(userID)
	if err != nil {
		return nil, err
	}

	for _, p := range userPeers {
		aclPeers, _ := account.getPeerConnectionResources(p.ID)
		for _, aclPeer := range aclPeers {
			if aclPeer.ID == peerID {
				return peer, nil
			}
		}
	}

	return nil, status.Errorf(status.Internal, "user %s has no access to peer %s under account %s", userID, peerID, accountID)
}

func updatePeerMeta(peer *Peer, meta PeerSystemMeta, account *Account) (*Peer, bool) {
	if peer.UpdateMetaIfNew(meta) {
		account.UpdatePeer(peer)
		return peer, true
	}
	return peer, false
}

// updateAccountPeers updates all peers that belong to an account.
// Should be called when changes have to be synced to peers.
func (am *DefaultAccountManager) updateAccountPeers(account *Account) {
	peers := account.GetPeers()

	for _, peer := range peers {
		remotePeerNetworkMap := account.GetPeerNetworkMap(peer.ID, am.dnsDomain)
		update := toSyncResponse(nil, peer, nil, remotePeerNetworkMap, am.GetDNSDomain())
		am.peersUpdateManager.SendUpdate(peer.ID, &UpdateMessage{Update: update})
	}
}
