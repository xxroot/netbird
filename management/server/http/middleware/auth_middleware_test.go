package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt"

	"github.com/netbirdio/netbird/management/server"
	"github.com/netbirdio/netbird/management/server/jwtclaims"
)

const (
	audience    = "audience"
	userIDClaim = "userIDClaim"
	accountID   = "accountID"
	domain      = "domain"
	userID      = "userID"
	tokenID     = "tokenID"
	PAT         = "nbp_PAT"
	JWT         = "JWT"
	wrongToken  = "wrongToken"
)

var testAccount = &server.Account{
	Id:     accountID,
	Domain: domain,
	Users: map[string]*server.User{
		userID: {
			Id: userID,
			PATs: map[string]*server.PersonalAccessToken{
				tokenID: {
					ID:             tokenID,
					Name:           "My first token",
					HashedToken:    "someHash",
					ExpirationDate: time.Now().UTC().AddDate(0, 0, 7),
					CreatedBy:      userID,
					CreatedAt:      time.Now().UTC(),
					LastUsed:       time.Now().UTC(),
				},
			},
		},
	},
}

func mockGetAccountFromPAT(token string) (*server.Account, *server.User, *server.PersonalAccessToken, error) {
	if token == PAT {
		return testAccount, testAccount.Users[userID], testAccount.Users[userID].PATs[tokenID], nil
	}
	return nil, nil, nil, fmt.Errorf("PAT invalid")
}

func mockValidateAndParseToken(token string) (*jwt.Token, error) {
	if token == JWT {
		return &jwt.Token{
			Claims: jwt.MapClaims{
				userIDClaim:                          userID,
				audience + jwtclaims.AccountIDSuffix: accountID,
			},
			Valid: true,
		}, nil
	}
	return nil, fmt.Errorf("JWT invalid")
}

func mockMarkPATUsed(token string) error {
	if token == tokenID {
		return nil
	}
	return fmt.Errorf("Should never get reached")
}

func mockCheckUserAccessByJWTGroups(claims jwtclaims.AuthorizationClaims) error {
	if testAccount.Id != claims.AccountId {
		return fmt.Errorf("account with id %s does not exist", claims.AccountId)
	}

	if _, ok := testAccount.Users[claims.UserId]; !ok {
		return fmt.Errorf("user with id %s does not exist", claims.UserId)
	}

	return nil
}

func TestAuthMiddleware_Handler(t *testing.T) {
	tt := []struct {
		name               string
		authHeader         string
		expectedStatusCode int
	}{
		{
			name:               "Valid PAT Token",
			authHeader:         "Token " + PAT,
			expectedStatusCode: 200,
		},
		{
			name:               "Invalid PAT Token",
			authHeader:         "Token " + wrongToken,
			expectedStatusCode: 401,
		},
		{
			name:               "Fallback to PAT Token",
			authHeader:         "Bearer " + PAT,
			expectedStatusCode: 200,
		},
		{
			name:               "Valid JWT Token",
			authHeader:         "Bearer " + JWT,
			expectedStatusCode: 200,
		},
		{
			name:               "Invalid JWT Token",
			authHeader:         "Bearer " + wrongToken,
			expectedStatusCode: 401,
		},
		{
			name:               "Basic Auth",
			authHeader:         "Basic  " + PAT,
			expectedStatusCode: 401,
		},
	}

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// do nothing
	})

	claimsExtractor := jwtclaims.NewClaimsExtractor(
		jwtclaims.WithAudience(audience),
		jwtclaims.WithUserIDClaim(userIDClaim),
	)

	authMiddleware := NewAuthMiddleware(
		mockGetAccountFromPAT,
		mockValidateAndParseToken,
		mockMarkPATUsed,
		mockCheckUserAccessByJWTGroups,
		claimsExtractor,
		audience,
		userIDClaim,
	)

	handlerToTest := authMiddleware.Handler(nextHandler)

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://testing", nil)
			req.Header.Set("Authorization", tc.authHeader)
			rec := httptest.NewRecorder()

			handlerToTest.ServeHTTP(rec, req)

			result := rec.Result()
			defer result.Body.Close()
			if result.StatusCode != tc.expectedStatusCode {
				t.Errorf("expected status code %d, got %d", tc.expectedStatusCode, result.StatusCode)
			}
		})
	}

}
