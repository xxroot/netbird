package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/netbirdio/netbird/management/server/activity"
	"github.com/netbirdio/netbird/management/server/idp"
	"github.com/netbirdio/netbird/management/server/jwtclaims"
	"github.com/netbirdio/netbird/management/server/status"
)

const (
	UserRoleAdmin   UserRole = "admin"
	UserRoleUser    UserRole = "user"
	UserRoleUnknown UserRole = "unknown"

	UserStatusActive   UserStatus = "active"
	UserStatusDisabled UserStatus = "disabled"
	UserStatusInvited  UserStatus = "invited"
)

// StrRoleToUserRole returns UserRole for a given strRole or UserRoleUnknown if the specified role is unknown
func StrRoleToUserRole(strRole string) UserRole {
	switch strings.ToLower(strRole) {
	case "admin":
		return UserRoleAdmin
	case "user":
		return UserRoleUser
	default:
		return UserRoleUnknown
	}
}

// UserStatus is the status of a User
type UserStatus string

// UserRole is the role of a User
type UserRole string

// User represents a user of the system
type User struct {
	Id string `gorm:"primaryKey"`
	// AccountID is a reference to Account that this object belongs
	AccountID     string `json:"-" gorm:"index"`
	Role          UserRole
	IsServiceUser bool
	// ServiceUserName is only set if IsServiceUser is true
	ServiceUserName string
	// AutoGroups is a list of Group IDs to auto-assign to peers registered by this user
	AutoGroups []string                        `gorm:"serializer:json"`
	PATs       map[string]*PersonalAccessToken `gorm:"-"`
	PATsG      []PersonalAccessToken           `json:"-" gorm:"foreignKey:UserID;references:id"`
	// Blocked indicates whether the user is blocked. Blocked users can't use the system.
	Blocked bool
	// LastLogin is the last time the user logged in to IdP
	LastLogin time.Time
}

// IsBlocked returns true if the user is blocked, false otherwise
func (u *User) IsBlocked() bool {
	return u.Blocked
}

func (u *User) LastDashboardLoginChanged(LastLogin time.Time) bool {
	return LastLogin.After(u.LastLogin) && !u.LastLogin.IsZero()
}

// IsAdmin returns true if the user is an admin, false otherwise
func (u *User) IsAdmin() bool {
	return u.Role == UserRoleAdmin
}

// ToUserInfo converts a User object to a UserInfo object.
func (u *User) ToUserInfo(userData *idp.UserData) (*UserInfo, error) {
	autoGroups := u.AutoGroups
	if autoGroups == nil {
		autoGroups = []string{}
	}

	if userData == nil {
		return &UserInfo{
			ID:            u.Id,
			Email:         "",
			Name:          u.ServiceUserName,
			Role:          string(u.Role),
			AutoGroups:    u.AutoGroups,
			Status:        string(UserStatusActive),
			IsServiceUser: u.IsServiceUser,
			IsBlocked:     u.Blocked,
			LastLogin:     u.LastLogin,
		}, nil
	}
	if userData.ID != u.Id {
		return nil, fmt.Errorf("wrong UserData provided for user %s", u.Id)
	}

	userStatus := UserStatusActive
	if userData.AppMetadata.WTPendingInvite != nil && *userData.AppMetadata.WTPendingInvite {
		userStatus = UserStatusInvited
	}

	return &UserInfo{
		ID:            u.Id,
		Email:         userData.Email,
		Name:          userData.Name,
		Role:          string(u.Role),
		AutoGroups:    autoGroups,
		Status:        string(userStatus),
		IsServiceUser: u.IsServiceUser,
		IsBlocked:     u.Blocked,
		LastLogin:     u.LastLogin,
	}, nil
}

// Copy the user
func (u *User) Copy() *User {
	autoGroups := make([]string, len(u.AutoGroups))
	copy(autoGroups, u.AutoGroups)
	pats := make(map[string]*PersonalAccessToken, len(u.PATs))
	for k, v := range u.PATs {
		pats[k] = v.Copy()
	}
	return &User{
		Id:              u.Id,
		AccountID:       u.AccountID,
		Role:            u.Role,
		AutoGroups:      autoGroups,
		IsServiceUser:   u.IsServiceUser,
		ServiceUserName: u.ServiceUserName,
		PATs:            pats,
		Blocked:         u.Blocked,
		LastLogin:       u.LastLogin,
	}
}

// NewUser creates a new user
func NewUser(id string, role UserRole, isServiceUser bool, serviceUserName string, autoGroups []string) *User {
	return &User{
		Id:              id,
		Role:            role,
		IsServiceUser:   isServiceUser,
		ServiceUserName: serviceUserName,
		AutoGroups:      autoGroups,
	}
}

// NewRegularUser creates a new user with role UserRoleUser
func NewRegularUser(id string) *User {
	return NewUser(id, UserRoleUser, false, "", []string{})
}

// NewAdminUser creates a new user with role UserRoleAdmin
func NewAdminUser(id string) *User {
	return NewUser(id, UserRoleAdmin, false, "", []string{})
}

// createServiceUser creates a new service user under the given account.
func (am *DefaultAccountManager) createServiceUser(accountID string, initiatorUserID string, role UserRole, serviceUserName string, autoGroups []string) (*UserInfo, error) {
	unlock := am.Store.AcquireAccountLock(accountID)
	defer unlock()

	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return nil, status.Errorf(status.NotFound, "account %s doesn't exist", accountID)
	}

	executingUser := account.Users[initiatorUserID]
	if executingUser == nil {
		return nil, status.Errorf(status.NotFound, "user not found")
	}
	if executingUser.Role != UserRoleAdmin {
		return nil, status.Errorf(status.PermissionDenied, "only admins can create service users")
	}

	newUserID := uuid.New().String()
	newUser := NewUser(newUserID, role, true, serviceUserName, autoGroups)
	log.Debugf("New User: %v", newUser)
	account.Users[newUserID] = newUser

	err = am.Store.SaveAccount(account)
	if err != nil {
		return nil, err
	}

	meta := map[string]any{"name": newUser.ServiceUserName}
	am.storeEvent(initiatorUserID, newUser.Id, accountID, activity.ServiceUserCreated, meta)

	return &UserInfo{
		ID:            newUser.Id,
		Email:         "",
		Name:          newUser.ServiceUserName,
		Role:          string(newUser.Role),
		AutoGroups:    newUser.AutoGroups,
		Status:        string(UserStatusActive),
		IsServiceUser: true,
		LastLogin:     time.Time{},
	}, nil
}

// CreateUser creates a new user under the given account. Effectively this is a user invite.
func (am *DefaultAccountManager) CreateUser(accountID, userID string, user *UserInfo) (*UserInfo, error) {
	if user.IsServiceUser {
		return am.createServiceUser(accountID, userID, StrRoleToUserRole(user.Role), user.Name, user.AutoGroups)
	}
	return am.inviteNewUser(accountID, userID, user)
}

// inviteNewUser Invites a USer to a given account and creates reference in datastore
func (am *DefaultAccountManager) inviteNewUser(accountID, userID string, invite *UserInfo) (*UserInfo, error) {
	unlock := am.Store.AcquireAccountLock(accountID)
	defer unlock()

	if am.idpManager == nil {
		return nil, status.Errorf(status.PreconditionFailed, "IdP manager must be enabled to send user invites")
	}

	if invite == nil {
		return nil, fmt.Errorf("provided user update is nil")
	}

	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return nil, status.Errorf(status.NotFound, "account %s doesn't exist", accountID)
	}

	// initiator is the one who is inviting the new user
	initiatorUser, err := am.lookupUserInCache(userID, account)
	if err != nil {
		return nil, status.Errorf(status.NotFound, "user %s doesn't exist in IdP", userID)
	}

	// check if the user is already registered with this email => reject
	user, err := am.lookupUserInCacheByEmail(invite.Email, accountID)
	if err != nil {
		return nil, err
	}

	if user != nil {
		return nil, status.Errorf(status.UserAlreadyExists, "can't invite a user with an existing NetBird account")
	}

	users, err := am.idpManager.GetUserByEmail(invite.Email)
	if err != nil {
		return nil, err
	}

	if len(users) > 0 {
		return nil, status.Errorf(status.UserAlreadyExists, "can't invite a user with an existing NetBird account")
	}

	idpUser, err := am.idpManager.CreateUser(invite.Email, invite.Name, accountID, initiatorUser.Email)
	if err != nil {
		return nil, err
	}

	role := StrRoleToUserRole(invite.Role)
	newUser := &User{
		Id:         idpUser.ID,
		Role:       role,
		AutoGroups: invite.AutoGroups,
	}
	account.Users[idpUser.ID] = newUser

	err = am.Store.SaveAccount(account)
	if err != nil {
		return nil, err
	}

	_, err = am.refreshCache(account.Id)
	if err != nil {
		return nil, err
	}

	am.storeEvent(userID, newUser.Id, accountID, activity.UserInvited, nil)

	return newUser.ToUserInfo(idpUser)
}

// GetUser looks up a user by provided authorization claims.
// It will also create an account if didn't exist for this user before.
func (am *DefaultAccountManager) GetUser(claims jwtclaims.AuthorizationClaims) (*User, error) {
	account, _, err := am.GetAccountFromToken(claims)
	if err != nil {
		return nil, fmt.Errorf("failed to get account with token claims %v", err)
	}

	user, ok := account.Users[claims.UserId]
	if !ok {
		return nil, status.Errorf(status.NotFound, "user not found")
	}

	// this code should be outside of the am.GetAccountFromToken(claims) because this method is called also by the gRPC
	// server when user authenticates a device. And we need to separate the Dashboard login event from the Device login event.
	unlock := am.Store.AcquireAccountLock(account.Id)
	newLogin := user.LastDashboardLoginChanged(claims.LastLogin)
	err = am.Store.SaveUserLastLogin(account.Id, claims.UserId, claims.LastLogin)
	unlock()
	if newLogin {
		meta := map[string]any{"timestamp": claims.LastLogin}
		am.storeEvent(claims.UserId, claims.UserId, account.Id, activity.DashboardLogin, meta)
		if err != nil {
			log.Errorf("failed saving user last login: %v", err)
		}
	}

	return user, nil
}

func (am *DefaultAccountManager) deleteServiceUser(account *Account, initiatorUserID string, targetUser *User) {
	meta := map[string]any{"name": targetUser.ServiceUserName}
	am.storeEvent(initiatorUserID, targetUser.Id, account.Id, activity.ServiceUserDeleted, meta)
	delete(account.Users, targetUser.Id)
}

// DeleteUser deletes a user from the given account.
func (am *DefaultAccountManager) DeleteUser(accountID, initiatorUserID string, targetUserID string) error {
	if initiatorUserID == targetUserID {
		return status.Errorf(status.InvalidArgument, "self deletion is not allowed")
	}
	unlock := am.Store.AcquireAccountLock(accountID)
	defer unlock()

	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return err
	}

	executingUser := account.Users[initiatorUserID]
	if executingUser == nil {
		return status.Errorf(status.NotFound, "user not found")
	}
	if executingUser.Role != UserRoleAdmin {
		return status.Errorf(status.PermissionDenied, "only admins can delete users")
	}

	targetUser := account.Users[targetUserID]
	if targetUser == nil {
		return status.Errorf(status.NotFound, "target user not found")
	}

	// handle service user first and exit, no need to fetch extra data from IDP, etc
	if targetUser.IsServiceUser {
		am.deleteServiceUser(account, initiatorUserID, targetUser)
		return am.Store.SaveAccount(account)
	}

	return am.deleteRegularUser(account, initiatorUserID, targetUserID)
}

func (am *DefaultAccountManager) deleteRegularUser(account *Account, initiatorUserID, targetUserID string) error {
	tuEmail, tuName, err := am.getEmailAndNameOfTargetUser(account.Id, initiatorUserID, targetUserID)
	if err != nil {
		log.Errorf("failed to resolve email address: %s", err)
		return err
	}

	if !isNil(am.idpManager) {
		err = am.deleteUserFromIDP(targetUserID, account.Id)
		if err != nil {
			log.Debugf("failed to delete user from IDP: %s", targetUserID)
			return err
		}
	}

	err = am.deleteUserPeers(initiatorUserID, targetUserID, account)
	if err != nil {
		return err
	}

	delete(account.Users, targetUserID)
	err = am.Store.SaveAccount(account)
	if err != nil {
		return err
	}

	meta := map[string]any{"name": tuName, "email": tuEmail}
	am.storeEvent(initiatorUserID, targetUserID, account.Id, activity.UserDeleted, meta)

	am.updateAccountPeers(account)

	return nil
}

func (am *DefaultAccountManager) deleteUserPeers(initiatorUserID string, targetUserID string, account *Account) error {
	peers, err := account.FindUserPeers(targetUserID)
	if err != nil {
		return status.Errorf(status.Internal, "failed to find user peers")
	}

	peerIDs := make([]string, 0, len(peers))
	for _, peer := range peers {
		peerIDs = append(peerIDs, peer.ID)
	}

	return am.deletePeers(account, peerIDs, initiatorUserID)
}

// InviteUser resend invitations to users who haven't activated their accounts prior to the expiration period.
func (am *DefaultAccountManager) InviteUser(accountID string, initiatorUserID string, targetUserID string) error {
	unlock := am.Store.AcquireAccountLock(accountID)
	defer unlock()

	if am.idpManager == nil {
		return status.Errorf(status.PreconditionFailed, "IdP manager must be enabled to send user invites")
	}

	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return status.Errorf(status.NotFound, "account %s doesn't exist", accountID)
	}

	// check if the user is already registered with this ID
	user, err := am.lookupUserInCache(targetUserID, account)
	if err != nil {
		return err
	}

	if user == nil {
		return status.Errorf(status.NotFound, "user account %s doesn't exist", targetUserID)
	}

	// check if user account is already invited and account is not activated
	pendingInvite := user.AppMetadata.WTPendingInvite
	if pendingInvite == nil || !*pendingInvite {
		return status.Errorf(status.PreconditionFailed, "can't invite a user with an activated NetBird account")
	}

	err = am.idpManager.InviteUserByID(user.ID)
	if err != nil {
		return err
	}

	am.storeEvent(initiatorUserID, user.ID, accountID, activity.UserInvited, nil)

	return nil
}

// CreatePAT creates a new PAT for the given user
func (am *DefaultAccountManager) CreatePAT(accountID string, initiatorUserID string, targetUserID string, tokenName string, expiresIn int) (*PersonalAccessTokenGenerated, error) {
	unlock := am.Store.AcquireAccountLock(accountID)
	defer unlock()

	if tokenName == "" {
		return nil, status.Errorf(status.InvalidArgument, "token name can't be empty")
	}

	if expiresIn < 1 || expiresIn > 365 {
		return nil, status.Errorf(status.InvalidArgument, "expiration has to be between 1 and 365")
	}

	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return nil, err
	}

	targetUser, ok := account.Users[targetUserID]
	if !ok {
		return nil, status.Errorf(status.NotFound, "user not found")
	}

	executingUser, ok := account.Users[initiatorUserID]
	if !ok {
		return nil, status.Errorf(status.NotFound, "user not found")
	}

	if !(initiatorUserID == targetUserID || (executingUser.IsAdmin() && targetUser.IsServiceUser)) {
		return nil, status.Errorf(status.PermissionDenied, "no permission to create PAT for this user")
	}

	pat, err := CreateNewPAT(tokenName, expiresIn, executingUser.Id)
	if err != nil {
		return nil, status.Errorf(status.Internal, "failed to create PAT: %v", err)
	}

	targetUser.PATs[pat.ID] = &pat.PersonalAccessToken

	err = am.Store.SaveAccount(account)
	if err != nil {
		return nil, status.Errorf(status.Internal, "failed to save account: %v", err)
	}

	meta := map[string]any{"name": pat.Name, "is_service_user": targetUser.IsServiceUser, "user_name": targetUser.ServiceUserName}
	am.storeEvent(initiatorUserID, targetUserID, accountID, activity.PersonalAccessTokenCreated, meta)

	return pat, nil
}

// DeletePAT deletes a specific PAT from a user
func (am *DefaultAccountManager) DeletePAT(accountID string, initiatorUserID string, targetUserID string, tokenID string) error {
	unlock := am.Store.AcquireAccountLock(accountID)
	defer unlock()

	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return status.Errorf(status.NotFound, "account not found: %s", err)
	}

	targetUser, ok := account.Users[targetUserID]
	if !ok {
		return status.Errorf(status.NotFound, "user not found")
	}

	executingUser, ok := account.Users[initiatorUserID]
	if !ok {
		return status.Errorf(status.NotFound, "user not found")
	}

	if !(initiatorUserID == targetUserID || (executingUser.IsAdmin() && targetUser.IsServiceUser)) {
		return status.Errorf(status.PermissionDenied, "no permission to delete PAT for this user")
	}

	pat := targetUser.PATs[tokenID]
	if pat == nil {
		return status.Errorf(status.NotFound, "PAT not found")
	}

	err = am.Store.DeleteTokenID2UserIDIndex(pat.ID)
	if err != nil {
		return status.Errorf(status.Internal, "Failed to delete token id index: %s", err)
	}
	err = am.Store.DeleteHashedPAT2TokenIDIndex(pat.HashedToken)
	if err != nil {
		return status.Errorf(status.Internal, "Failed to delete hashed token index: %s", err)
	}

	meta := map[string]any{"name": pat.Name, "is_service_user": targetUser.IsServiceUser, "user_name": targetUser.ServiceUserName}
	am.storeEvent(initiatorUserID, targetUserID, accountID, activity.PersonalAccessTokenDeleted, meta)

	delete(targetUser.PATs, tokenID)

	err = am.Store.SaveAccount(account)
	if err != nil {
		return status.Errorf(status.Internal, "Failed to save account: %s", err)
	}
	return nil
}

// GetPAT returns a specific PAT from a user
func (am *DefaultAccountManager) GetPAT(accountID string, initiatorUserID string, targetUserID string, tokenID string) (*PersonalAccessToken, error) {
	unlock := am.Store.AcquireAccountLock(accountID)
	defer unlock()

	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return nil, status.Errorf(status.NotFound, "account not found: %s", err)
	}

	targetUser, ok := account.Users[targetUserID]
	if !ok {
		return nil, status.Errorf(status.NotFound, "user not found")
	}

	executingUser, ok := account.Users[initiatorUserID]
	if !ok {
		return nil, status.Errorf(status.NotFound, "user not found")
	}

	if !(initiatorUserID == targetUserID || (executingUser.IsAdmin() && targetUser.IsServiceUser)) {
		return nil, status.Errorf(status.PermissionDenied, "no permission to get PAT for this userser")
	}

	pat := targetUser.PATs[tokenID]
	if pat == nil {
		return nil, status.Errorf(status.NotFound, "PAT not found")
	}

	return pat, nil
}

// GetAllPATs returns all PATs for a user
func (am *DefaultAccountManager) GetAllPATs(accountID string, initiatorUserID string, targetUserID string) ([]*PersonalAccessToken, error) {
	unlock := am.Store.AcquireAccountLock(accountID)
	defer unlock()

	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return nil, status.Errorf(status.NotFound, "account not found: %s", err)
	}

	targetUser, ok := account.Users[targetUserID]
	if !ok {
		return nil, status.Errorf(status.NotFound, "user not found")
	}

	executingUser, ok := account.Users[initiatorUserID]
	if !ok {
		return nil, status.Errorf(status.NotFound, "user not found")
	}

	if !(initiatorUserID == targetUserID || (executingUser.IsAdmin() && targetUser.IsServiceUser)) {
		return nil, status.Errorf(status.PermissionDenied, "no permission to get PAT for this user")
	}

	var pats []*PersonalAccessToken
	for _, pat := range targetUser.PATs {
		pats = append(pats, pat)
	}

	return pats, nil
}

// SaveUser saves updates to the given user. If the user doesn't exit it will throw status.NotFound error.
// Only User.AutoGroups, User.Role, and User.Blocked fields are allowed to be updated for now.
func (am *DefaultAccountManager) SaveUser(accountID, initiatorUserID string, update *User) (*UserInfo, error) {
	unlock := am.Store.AcquireAccountLock(accountID)
	defer unlock()

	if update == nil {
		return nil, status.Errorf(status.InvalidArgument, "provided user update is nil")
	}

	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return nil, err
	}

	initiatorUser, err := account.FindUser(initiatorUserID)
	if err != nil {
		return nil, err
	}

	if !initiatorUser.IsAdmin() || initiatorUser.IsBlocked() {
		return nil, status.Errorf(status.PermissionDenied, "only admins are authorized to perform user update operations")
	}

	oldUser := account.Users[update.Id]
	if oldUser == nil {
		return nil, status.Errorf(status.NotFound, "user to update doesn't exist")
	}

	if initiatorUser.IsAdmin() && initiatorUserID == update.Id && oldUser.Blocked != update.Blocked {
		return nil, status.Errorf(status.PermissionDenied, "admins can't block or unblock themselves")
	}

	if initiatorUser.IsAdmin() && initiatorUserID == update.Id && update.Role != UserRoleAdmin {
		return nil, status.Errorf(status.PermissionDenied, "admins can't change their role")
	}

	// only auto groups, revoked status, and name can be updated for now
	newUser := oldUser.Copy()
	newUser.Role = update.Role
	newUser.Blocked = update.Blocked

	for _, newGroupID := range update.AutoGroups {
		if _, ok := account.Groups[newGroupID]; !ok {
			return nil, status.Errorf(status.InvalidArgument, "provided group ID %s in the user %s update doesn't exist",
				newGroupID, update.Id)
		}
	}
	newUser.AutoGroups = update.AutoGroups

	account.Users[newUser.Id] = newUser

	if !oldUser.IsBlocked() && update.IsBlocked() {
		// expire peers that belong to the user who's getting blocked
		blockedPeers, err := account.FindUserPeers(update.Id)
		if err != nil {
			return nil, err
		}

		if err := am.expireAndUpdatePeers(account, blockedPeers); err != nil {
			log.Errorf("failed update expired peers: %s", err)
			return nil, err
		}
	}

	if update.AutoGroups != nil && account.Settings.GroupsPropagationEnabled {
		removedGroups := difference(oldUser.AutoGroups, update.AutoGroups)
		// need force update all auto groups in any case they will not be dublicated
		account.UserGroupsAddToPeers(oldUser.Id, update.AutoGroups...)
		account.UserGroupsRemoveFromPeers(oldUser.Id, removedGroups...)

		account.Network.IncSerial()
		if err = am.Store.SaveAccount(account); err != nil {
			return nil, err
		}

		am.updateAccountPeers(account)
	} else {
		if err = am.Store.SaveAccount(account); err != nil {
			return nil, err
		}
	}

	defer func() {
		if oldUser.IsBlocked() != update.IsBlocked() {
			if update.IsBlocked() {
				am.storeEvent(initiatorUserID, oldUser.Id, accountID, activity.UserBlocked, nil)
			} else {
				am.storeEvent(initiatorUserID, oldUser.Id, accountID, activity.UserUnblocked, nil)
			}
		}

		// store activity logs
		if oldUser.Role != newUser.Role {
			am.storeEvent(initiatorUserID, oldUser.Id, accountID, activity.UserRoleUpdated, map[string]any{"role": newUser.Role})
		}

		if update.AutoGroups != nil {
			removedGroups := difference(oldUser.AutoGroups, update.AutoGroups)
			addedGroups := difference(newUser.AutoGroups, oldUser.AutoGroups)
			for _, g := range removedGroups {
				group := account.GetGroup(g)
				if group != nil {
					am.storeEvent(initiatorUserID, oldUser.Id, accountID, activity.GroupRemovedFromUser,
						map[string]any{"group": group.Name, "group_id": group.ID, "is_service_user": newUser.IsServiceUser, "user_name": newUser.ServiceUserName})
				} else {
					log.Errorf("group %s not found while saving user activity event of account %s", g, account.Id)
				}
			}

			for _, g := range addedGroups {
				group := account.GetGroup(g)
				if group != nil {
					am.storeEvent(initiatorUserID, oldUser.Id, accountID, activity.GroupAddedToUser,
						map[string]any{"group": group.Name, "group_id": group.ID, "is_service_user": newUser.IsServiceUser, "user_name": newUser.ServiceUserName})
				}
			}
		}
	}()

	if !isNil(am.idpManager) && !newUser.IsServiceUser {
		userData, err := am.lookupUserInCache(newUser.Id, account)
		if err != nil {
			return nil, err
		}
		if userData == nil {
			return nil, status.Errorf(status.NotFound, "user %s not found in the IdP", newUser.Id)
		}
		return newUser.ToUserInfo(userData)
	}
	return newUser.ToUserInfo(nil)
}

// GetOrCreateAccountByUser returns an existing account for a given user id or creates a new one if doesn't exist
func (am *DefaultAccountManager) GetOrCreateAccountByUser(userID, domain string) (*Account, error) {
	unlock := am.Store.AcquireGlobalLock()
	defer unlock()

	lowerDomain := strings.ToLower(domain)

	account, err := am.Store.GetAccountByUser(userID)
	if err != nil {
		if s, ok := status.FromError(err); ok && s.Type() == status.NotFound {
			account, err = am.newAccount(userID, lowerDomain)
			if err != nil {
				return nil, err
			}
			err = am.Store.SaveAccount(account)
			if err != nil {
				return nil, err
			}
		} else {
			// other error
			return nil, err
		}
	}

	userObj := account.Users[userID]

	if account.Domain != lowerDomain && userObj.Role == UserRoleAdmin {
		account.Domain = lowerDomain
		err = am.Store.SaveAccount(account)
		if err != nil {
			return nil, status.Errorf(status.Internal, "failed updating account with domain")
		}
	}

	return account, nil
}

// GetUsersFromAccount performs a batched request for users from IDP by account ID apply filter on what data to return
// based on provided user role.
func (am *DefaultAccountManager) GetUsersFromAccount(accountID, userID string) ([]*UserInfo, error) {
	account, err := am.Store.GetAccount(accountID)
	if err != nil {
		return nil, err
	}

	user, err := account.FindUser(userID)
	if err != nil {
		return nil, err
	}

	queriedUsers := make([]*idp.UserData, 0)
	if !isNil(am.idpManager) {
		users := make(map[string]struct{}, len(account.Users))
		for _, user := range account.Users {
			if !user.IsServiceUser {
				users[user.Id] = struct{}{}
			}
		}
		queriedUsers, err = am.lookupCache(users, accountID)
		if err != nil {
			return nil, err
		}
	}

	userInfos := make([]*UserInfo, 0)

	// in case of self-hosted, or IDP doesn't return anything, we will return the locally stored userInfo
	if len(queriedUsers) == 0 {
		for _, accountUser := range account.Users {
			if !user.IsAdmin() && user.Id != accountUser.Id {
				// if user is not an admin then show only current user and do not show other users
				continue
			}
			info, err := accountUser.ToUserInfo(nil)
			if err != nil {
				return nil, err
			}
			userInfos = append(userInfos, info)
		}
		return userInfos, nil
	}

	for _, localUser := range account.Users {
		if !user.IsAdmin() && user.Id != localUser.Id {
			// if user is not an admin then show only current user and do not show other users
			continue
		}

		var info *UserInfo
		if queriedUser, contains := findUserInIDPUserdata(localUser.Id, queriedUsers); contains {
			info, err = localUser.ToUserInfo(queriedUser)
			if err != nil {
				return nil, err
			}
		} else {
			name := ""
			if localUser.IsServiceUser {
				name = localUser.ServiceUserName
			}
			info = &UserInfo{
				ID:            localUser.Id,
				Email:         "",
				Name:          name,
				Role:          string(localUser.Role),
				AutoGroups:    localUser.AutoGroups,
				Status:        string(UserStatusActive),
				IsServiceUser: localUser.IsServiceUser,
			}
		}
		userInfos = append(userInfos, info)
	}

	return userInfos, nil
}

// expireAndUpdatePeers expires all peers of the given user and updates them in the account
func (am *DefaultAccountManager) expireAndUpdatePeers(account *Account, peers []*Peer) error {
	var peerIDs []string
	for _, peer := range peers {
		if peer.Status.LoginExpired {
			continue
		}
		peerIDs = append(peerIDs, peer.ID)
		peer.MarkLoginExpired(true)
		account.UpdatePeer(peer)
		if err := am.Store.SavePeerStatus(account.Id, peer.ID, *peer.Status); err != nil {
			return err
		}
		am.storeEvent(
			peer.UserID, peer.ID, account.Id,
			activity.PeerLoginExpired, peer.EventMeta(am.GetDNSDomain()),
		)
	}

	if len(peerIDs) != 0 {
		// this will trigger peer disconnect from the management service
		am.peersUpdateManager.CloseChannels(peerIDs)
		am.updateAccountPeers(account)
	}
	return nil
}

func (am *DefaultAccountManager) deleteUserFromIDP(targetUserID, accountID string) error {
	if am.userDeleteFromIDPEnabled {
		log.Debugf("user %s deleted from IdP", targetUserID)
		err := am.idpManager.DeleteUser(targetUserID)
		if err != nil {
			return fmt.Errorf("failed to delete user %s from IdP: %s", targetUserID, err)
		}
	} else {
		err := am.idpManager.UpdateUserAppMetadata(targetUserID, idp.AppMetadata{})
		if err != nil {
			return fmt.Errorf("failed to remove user %s app metadata in IdP: %s", targetUserID, err)
		}

		_, err = am.refreshCache(accountID)
		if err != nil {
			log.Errorf("refresh account (%q) cache: %v", accountID, err)
		}
	}
	return nil
}

func (am *DefaultAccountManager) getEmailAndNameOfTargetUser(accountId, initiatorId, targetId string) (string, string, error) {
	userInfos, err := am.GetUsersFromAccount(accountId, initiatorId)
	if err != nil {
		return "", "", err
	}
	for _, ui := range userInfos {
		if ui.ID == targetId {
			return ui.Email, ui.Name, nil
		}
	}

	return "", "", fmt.Errorf("user info not found for user: %s", targetId)
}

func findUserInIDPUserdata(userID string, userData []*idp.UserData) (*idp.UserData, bool) {
	for _, user := range userData {
		if user.ID == userID {
			return user, true
		}
	}
	return nil, false
}
