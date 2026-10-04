"""One-shot, preflighted final source fixes on the approved feature branch."""
from pathlib import Path

files = {}
def edit(path, old, new, count=1):
    text = files.get(path, Path(path).read_text(encoding='utf-8'))
    if text.count(old) != count:
        raise RuntimeError(f'{path}: expected {count}, found {text.count(old)}: {old[:100]!r}')
    files[path] = text.replace(old, new)
def new(path, content):
    if Path(path).exists():
        raise RuntimeError('Refusing to overwrite ' + path)
    files[path] = content

p='internal/gateway/context_compaction.go'
edit(p,'return &Request{ResolvedModel: q.ResolvedModel,','return &Request{Bridge: q.Bridge, ResolvedModel: q.ResolvedModel,')
edit(p,'}{summaryVersion, renderVersion, q.Model,','}{summaryVersion, renderVersion+":"+q.Bridge.InstructionPlacement, q.Model,')
edit(p,'\t\t\tif n <= summaryBudget {','\t\t\tif n <= summaryBudget && CheckTransportBudget(test)==nil {')
edit(p,'\t\tbuild := func() (string, error) {','\t\tif err:=CheckTransportBudget(sq);err!=nil{return err}\n\t\tbuild := func() (string, error) {')

p='internal/gateway/continuation.go'
edit(p,'type UpstreamState struct {\n','type UpstreamState struct {\n\tCredentialDigest string `json:"-"`\n')
edit(p,'type continuationRegistry struct {\n','type continuationRegistry struct {\n\tclosed bool\n')
edit(p,'func (c *continuationRegistry) clear() {\n\tc.mu.Lock()\n\tdefer c.mu.Unlock()','func (c *continuationRegistry) clear() {\n\tc.mu.Lock()\n\tdefer c.mu.Unlock()\n\tc.closed=true')
edit(p,'\tc := h.continuations\n\tc.mu.Lock()\n\tdefer c.mu.Unlock()','\tc := h.continuations\n\tc.mu.Lock()\n\tdefer c.mu.Unlock()\n\tif c.closed {return nil,&APIError{Status:503,Code:"gateway_closed",Stage:"continuation",Message:"Gateway is shutting down."}}')
edit(p,'\tif r.revoked || result == nil || result.Incomplete || !result.UpstreamState.usable() {','\tif c.closed || r.revoked || result == nil || result.Incomplete || !result.UpstreamState.usable() {')
edit(p,'\tif rec != nil && rec.busy {','\tif rec!=nil && session!="" && rec.session!="" && session!=rec.session {return nil,&APIError{Status:409,Code:"session_lineage_conflict",Message:"A live upstream conversation cannot be relabelled; use a new branch."}}\n\tif rec != nil && rec.busy {')

p='internal/facade/runner.go'
edit(p,'type RunRequest struct {\n','type RunRequest struct {\n\tGatewayCredentialDigest string\n\tGatewayCredentialSnapshot *creds.Credential\n')
edit(p,'\tres, err := r.runOnce(ctx, acct, req, emit)','\tif req.Isolated {ctx=withGatewayCredential(ctx,acct.ID,req.GatewayCredentialSnapshot)}\n\tres, err := r.runOnce(ctx, acct, req, emit)')
edit(p,'\tstarted := time.Now()\n\n\tcred := acct.Credential()','\tstarted := time.Now()\n\n\tcred := runnerCredential(ctx,acct)')
edit(p,'\tcred := acct.Credential()\n\tp := prism.Principal{Client: acct.Client, Cred: cred, ExtraHeaders: cred.Headers, AccountID: acct.ID}','\tcred := runnerCredential(ctx,acct)\n\tp := prism.Principal{Client: acct.Client, Cred: cred, ExtraHeaders: cred.Headers, AccountID: acct.ID}',3)
edit(p,'\tif req.UserID == "" && acct.Credential().UserID != "" {\n\t\treq.UserID = acct.Credential().UserID','\tif req.UserID == "" && cred.UserID != "" {\n\t\treq.UserID = cred.UserID')

p='internal/facade/gateway_lifecycle.go'
edit(p,'\t"context"','\t"context"\n\t"crypto/sha256"\n\t"encoding/hex"')
edit(p,'\t"github.com/oai-prism/oaiprism/internal/account"','\t"github.com/oai-prism/oaiprism/internal/account"\n\t"github.com/oai-prism/oaiprism/internal/creds"')
edit(p,'\tvalue, _ := r.accountEpoch.LoadOrStore(acct.ID, &atomic.Uint64{})','\treq.GatewayCredentialSnapshot=acct.Credential()\n\treq.GatewayCredentialDigest=gatewayCredentialDigest(req.GatewayCredentialSnapshot)\n\tvalue, _ := r.accountEpoch.LoadOrStore(acct.ID, &atomic.Uint64{})')
edit(p,'if state.AccountID != acct.ID || state.Epoch == 0 || epoch.Load() != state.Epoch {','if state.AccountID != acct.ID || state.Epoch == 0 || epoch.Load() != state.Epoch || state.CredentialDigest!=req.GatewayCredentialDigest {')
edit(p,'This account has advanced beyond the saved workspace. Retry with full history; the stale request was not executed.','The account workspace or credentials changed. Retry with full history; the stale request was not executed.')
edit(p,'return &gateway.UpstreamState{Epoch: req.GatewayEpoch,','return &gateway.UpstreamState{CredentialDigest:req.GatewayCredentialDigest, Epoch: req.GatewayEpoch,')
files[p] += r'''

// Pin an immutable credential snapshot through project creation, sandbox setup,
// synchronization, uploads, start and stop. A refresh cannot mix identities
// halfway through a request, and the next continuation observes the new digest.
type gatewayCredentialContextKey struct{}
type pinnedGatewayCredential struct {accountID string; credential *creds.Credential}
func withGatewayCredential(ctx context.Context, accountID string, credential *creds.Credential)context.Context {
 return context.WithValue(ctx,gatewayCredentialContextKey{},pinnedGatewayCredential{accountID,credential})
}
func runnerCredential(ctx context.Context,acct *account.Account)*creds.Credential {
 if pinned,ok:=ctx.Value(gatewayCredentialContextKey{}).(pinnedGatewayCredential);ok&&pinned.accountID==acct.ID{return pinned.credential}
 return acct.Credential()
}
func gatewayCredentialDigest(credential *creds.Credential)string {
 var raw []byte
 if credential!=nil {
  raw,_=json.Marshal(struct {Account,User,Access,Cookie string;Headers map[string]string}{credential.AccountID,credential.UserID,credential.AccessToken,credential.EffectiveCookie(),credential.Headers})
 }
 sum:=sha256.Sum256(raw);return hex.EncodeToString(sum[:])
}
'''
p='internal/facade/gateway_lifecycle_test.go'
edit(p,'state := &gateway.UpstreamState{Epoch: first.GatewayEpoch,','state := &gateway.UpstreamState{CredentialDigest:first.GatewayCredentialDigest, Epoch: first.GatewayEpoch,')

p='.github/workflows/gateway-sdk.yml'
edit(p,"'tools/context*',","'tools/context*', 'tools/bridge_canary.py', 'tools/test_bridge_canary.py',")
edit(p,'      - name: Install pinned official SDKs','      - name: Verify read-only canary defaults and bounded opt-in\n        run: python -m unittest discover -s tools -p test_bridge_canary.py -v\n      - name: Install pinned official SDKs')

new('internal/gateway/bridge_final_test.go',r'''package gateway

import (
 "context"
 "strings"
 "testing"
)
func TestSummaryInheritsBridgeBudgetWithoutUpstreamCursor(t *testing.T){
 q:=&Request{Model:"test-model",Bridge:BridgePolicy{MaxStartBytes:4096,InstructionPlacement:"user_relay",UploadMode:"raw",Continuation:ContinuationPolicy{Enabled:true}},UpstreamState:cursorFixture(),ContinuationOffset:7,StoredConversation:true}
 sq:=summaryRequest(q,nil,[]Item{{Type:"message",Role:"user",Content:[]Content{{Type:"input_text",Text:strings.Repeat("x",5000)}}}},"",128)
 if sq.Bridge.MaxStartBytes!=4096||sq.Bridge.UploadMode!="raw"||sq.Bridge.InstructionPlacement!="user_relay"{t.Fatal("summary lost transport policy")}
 if !sq.InternalSummary||sq.StoredConversation||sq.UpstreamState!=nil||sq.ContinuationOffset!=0||sq.Store{t.Fatal("summary inherited a mutable conversation cursor")}
 if CheckTransportBudget(sq)==nil{t.Fatal("summary escaped transport byte cap")}
}
func TestSummaryDoesNotStartWhenIndivisibleTurnExceedsByteBudget(t *testing.T){
 calls:=0
 h:=harness(t,func(context.Context,*Request,func()error,func(Delta)error)(*Result,error){calls++;return &Result{Text:"summary"},nil},nil)
 h.options.Context.Enabled=true;h.options.Context.WindowTokens=32768;h.options.Context.KeepLastTurns=1;h.options.Context.SummaryTokens=128
 q:=&Request{Model:"test-model",Format:"text",Bridge:BridgePolicy{MaxStartBytes:4096},Items:[]Item{
  {Type:"message",Role:"user",Content:[]Content{{Type:"input_text",Text:strings.Repeat("long_old_payload",600)}}},
  {Type:"message",Role:"assistant",Content:[]Content{{Type:"output_text",Text:"old reply"}}},
  {Type:"message",Role:"user",Content:[]Content{{Type:"input_text",Text:"new question"}}},
 }}
 before:=continuationHistoryHash(q.Items)
 if err:=h.prepareContext(context.Background(),q,"owner",true,nil);err==nil{t.Fatal("oversized summary turn accepted")}
 if calls!=0||continuationHistoryHash(q.Items)!=before{t.Fatal("summary started or history changed despite budget refusal",calls)}
}
func TestContinuationCannotBeRecreatedAfterRegistryClose(t *testing.T){
 h:=continuationHarness(t);q:=continuationRequest(h)
 lease,err:=h.beginContinuation(q,"owner","resp-one",nil);if err!=nil{t.Fatal(err)}
 h.continuations.clear()
 lease.complete(q,&Result{UpstreamState:cursorFixture()},answered(q))
 if len(h.continuations.records)!=0||len(h.continuations.sessions)!=0{t.Fatal("in-flight completion reopened closed registry")}
 if _,err=h.beginContinuation(continuationRequest(h),"owner","resp-two",nil);err==nil||publicError(err).Status!=503{t.Fatal("closed registry accepted new state",err)}
}
''')
new('internal/facade/gateway_credential_test.go',r'''package facade

import (
 "context"
 "errors"
 "strings"
 "testing"

 "github.com/oai-prism/oaiprism/internal/account"
 "github.com/oai-prism/oaiprism/internal/creds"
 "github.com/oai-prism/oaiprism/internal/gateway"
)
func TestGatewayCredentialRotationInvalidatesCursorWithoutEpochChange(t *testing.T){
 r:=&Runner{};acct:=&account.Account{ID:"stable-account-id"}
 original:=&creds.Credential{UserID:"original-user",AccessToken:"original-private-token",Headers:map[string]string{"X-Device":"one"}}
 acct.StoreCredential(original);first:=&RunRequest{Isolated:true}
 if err:=r.fenceGatewayAccount(acct,first);err!=nil{t.Fatal(err)}
 state:=&gateway.UpstreamState{Epoch:first.GatewayEpoch,AccountID:acct.ID,CredentialDigest:first.GatewayCredentialDigest}
 acct.StoreCredential(&creds.Credential{UserID:"another-user",AccessToken:"rotated-private-token"})
 var public *gateway.APIError
 if err:=r.fenceGatewayAccount(acct,&RunRequest{Isolated:true,GatewayResume:state});!errors.As(err,&public)||public.Status!=409||strings.Contains(err.Error(),"private-token"){t.Fatal("credential rotation reused or exposed cursor",err)}
 pinned:=withGatewayCredential(context.Background(),acct.ID,original)
 if runnerCredential(pinned,acct)!=original{t.Fatal("request mixed old/new credential snapshots")}
 other:=&account.Account{ID:"other"};second:=&creds.Credential{AccessToken:"other-token"};other.StoreCredential(second)
 if runnerCredential(pinned,other)!=second{t.Fatal("credential context leaked across accounts")}
}
func TestCredentialDigestIncludesCookieIdentityAndHeaders(t *testing.T){
 a:=&creds.Credential{AccountID:"a",UserID:"u",CookieHeader:"session=one",Headers:map[string]string{"A":"a","B":"b"}}
 b:=&creds.Credential{AccountID:"a",UserID:"u",CookieHeader:"session=one",Headers:map[string]string{"B":"b","A":"a"}}
 if gatewayCredentialDigest(a)!=gatewayCredentialDigest(b){t.Fatal("map insertion order changed digest")}
 b.CookieHeader="session=two";if gatewayCredentialDigest(a)==gatewayCredentialDigest(b){t.Fatal("cookie not bound")}
 b.CookieHeader=a.CookieHeader;b.Headers["B"]="changed";if gatewayCredentialDigest(a)==gatewayCredentialDigest(b){t.Fatal("device header not bound")}
}
''')
for path,content in files.items():
    Path(path).write_text(content,encoding='utf-8')
print('Final hardening applied to',len(files),'files')
