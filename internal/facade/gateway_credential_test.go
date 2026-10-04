package facade

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/oai-prism/oaiprism/internal/account"
	"github.com/oai-prism/oaiprism/internal/creds"
	"github.com/oai-prism/oaiprism/internal/gateway"
)

func TestGatewayCredentialRotationInvalidatesCursorWithoutEpochChange(t *testing.T) {
	r := &Runner{}
	acct := &account.Account{ID: "stable-account-id"}
	original := &creds.Credential{UserID: "original-user", AccessToken: "original-private-token", Headers: map[string]string{"X-Device": "one"}}
	acct.StoreCredential(original)
	first := &RunRequest{Isolated: true}
	if err := r.fenceGatewayAccount(acct, first); err != nil {
		t.Fatal(err)
	}
	state := &gateway.UpstreamState{Epoch: first.GatewayEpoch, AccountID: acct.ID, CredentialDigest: first.GatewayCredentialDigest}
	acct.StoreCredential(&creds.Credential{UserID: "another-user", AccessToken: "rotated-private-token"})
	var public *gateway.APIError
	if err := r.fenceGatewayAccount(acct, &RunRequest{Isolated: true, GatewayResume: state}); !errors.As(err, &public) || public.Status != 409 || strings.Contains(err.Error(), "private-token") {
		t.Fatal("credential rotation reused or exposed cursor", err)
	}
	pinned := withGatewayCredential(context.Background(), acct.ID, original)
	if runnerCredential(pinned, acct) != original {
		t.Fatal("request mixed old/new credential snapshots")
	}
	other := &account.Account{ID: "other"}
	second := &creds.Credential{AccessToken: "other-token"}
	other.StoreCredential(second)
	if runnerCredential(pinned, other) != second {
		t.Fatal("credential context leaked across accounts")
	}
}
func TestCredentialDigestIncludesCookieIdentityAndHeaders(t *testing.T) {
	a := &creds.Credential{AccountID: "a", UserID: "u", CookieHeader: "session=one", Headers: map[string]string{"A": "a", "B": "b"}}
	b := &creds.Credential{AccountID: "a", UserID: "u", CookieHeader: "session=one", Headers: map[string]string{"B": "b", "A": "a"}}
	if gatewayCredentialDigest(a) != gatewayCredentialDigest(b) {
		t.Fatal("map insertion order changed digest")
	}
	b.CookieHeader = "session=two"
	if gatewayCredentialDigest(a) == gatewayCredentialDigest(b) {
		t.Fatal("cookie not bound")
	}
	b.CookieHeader = a.CookieHeader
	b.Headers["B"] = "changed"
	if gatewayCredentialDigest(a) == gatewayCredentialDigest(b) {
		t.Fatal("device header not bound")
	}
}
