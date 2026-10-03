package gateway

import (
 "bytes"
 "context"
 "encoding/base64"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "testing"
 "time"
)

func TestStoreMemoryBoundsAndImmutableCopies(t *testing.T){
 s,err:=newStore(Options{});if err!=nil{t.Fatal(err)};defer s.Close();ctx:=context.Background();now:=time.Now();s.now=func()time.Time{return now}
 snapshot:=Snapshot{Response:json.RawMessage(`{"id":"one"}`),Items:[]Item{{Type:"message",Role:"user",Content:[]Content{{Type:"input_text",Text:"private text"}}}}}
 if err=s.Put(ctx,"one","owner",snapshot);err!=nil{t.Fatal(err)}
 copy,ok,err:=s.Get(ctx,"one","owner");if err!=nil||!ok{t.Fatal(err)};copy.Response[0]='x';copy.Items[0].Content[0].Text="mutated"
 copy,ok,err=s.Get(ctx,"one","owner");if err!=nil||!ok||!json.Valid(copy.Response)||copy.Items[0].Content[0].Text!="private text"{t.Fatal("mutable store reference")}
 if _,ok,err=s.Get(ctx,"one","other");err!=nil||ok{t.Fatal("cross-owner read")}
 now=now.Add(storeTTL);if _,ok,err=s.Get(ctx,"one","owner");err!=nil||ok{t.Fatal("expired state accessible")};if s.bytes!=0{t.Fatal("expired memory retained")}
 for i:=0;i<storeMaxEntries+10;i++{if err=s.Put(ctx,fmt.Sprint(i),"owner",snapshot);err!=nil{t.Fatal(err)};now=now.Add(time.Millisecond)}
 if len(s.memory)!=storeMaxEntries||s.bytes>storeMaxBytes{t.Fatal("unbounded response store")}
 large:=Snapshot{Response:json.RawMessage(`"`+string(bytes.Repeat([]byte("a"),storeEntryLimit))+`"`)};if s.Put(ctx,"large","owner",large)==nil{t.Fatal("oversized snapshot accepted")}
}

func TestEncryptedStoreRestartAndWrongKey(t *testing.T){
 dir:=t.TempDir();path:=filepath.Join(dir,"responses.sqlite")
 t.Setenv("GATEWAY_TEST_KEY",base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7},32)))
 o:=Options{StorePath:path,StoreKeyEnv:"GATEWAY_TEST_KEY"}
 ctx:=context.Background();s,err:=newStore(o);if err!=nil{t.Fatal(err)}
 secret:="unique-private-transcript-87435"
 snap:=Snapshot{Response:json.RawMessage(`{"id":"resp_saved"}`),Items:[]Item{{Type:"message",Role:"user",Content:[]Content{{Type:"input_text",Text:secret}}}}}
 if err=s.Put(ctx,"resp_saved","owner",snap);err!=nil{t.Fatal(err)}
 if err=s.Close();err!=nil{t.Fatal(err)}
 raw,err:=os.ReadFile(path);if err!=nil{t.Fatal(err)};if bytes.Contains(raw,[]byte(secret)){t.Fatal("persistent transcript stored in plaintext")}
 s,err=newStore(o);if err!=nil{t.Fatal(err)}
 got,ok,err:=s.Get(ctx,"resp_saved","owner");if err!=nil||!ok||got.Items[0].Content[0].Text!=secret{t.Fatalf("restart lost snapshot %+v %v %v",got,ok,err)}
 if _,ok,err=s.Get(ctx,"resp_saved","other");err!=nil||ok{t.Fatal("cross-owner persistent read")}
 if ok,err=s.Delete(ctx,"resp_saved","other");err!=nil||ok{t.Fatal("cross-owner delete")}
 _=s.Close();t.Setenv("GATEWAY_TEST_KEY",base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8},32)))
 s,err=newStore(o);if err!=nil{t.Fatal(err)};defer s.Close()
 if _,ok,err=s.Get(ctx,"resp_saved","owner");err==nil||ok{t.Fatal("wrong encryption key silently treated as valid or empty state")}
}
func TestPersistentDeletionAndExpiry(t *testing.T){
 t.Setenv("GATEWAY_TEST_KEY",base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3},32)))
 s,err:=newStore(Options{StorePath:filepath.Join(t.TempDir(),"responses.sqlite"),StoreKeyEnv:"GATEWAY_TEST_KEY"});if err!=nil{t.Fatal(err)};defer s.Close()
 now:=time.Now();s.now=func()time.Time{return now};ctx:=context.Background();snapshot:=Snapshot{Response:json.RawMessage(`{"id":"one"}`)}
 if err=s.Put(ctx,"one","owner",snapshot);err!=nil{t.Fatal(err)}
 if ok,err:=s.Delete(ctx,"one","owner");err!=nil||!ok{t.Fatal("delete",ok,err)}
 if _,ok,err:=s.Get(ctx,"one","owner");err!=nil||ok{t.Fatal("deleted row accessible")}
 if err=s.Put(ctx,"two","owner",snapshot);err!=nil{t.Fatal(err)};now=now.Add(storeTTL)
 if _,ok,err:=s.Get(ctx,"two","owner");err!=nil||ok{t.Fatal("expired row accessible")}
}
func TestStoreConfigurationFailsClosed(t *testing.T){
 o:=options();o.ResponseStore=true;if o.Validate()==nil{t.Fatal("shared key alone authorized state storage")}
 o.TenantHeader="X-Tenant";o.TrustedPeers=[]string{"bad-cidr"};if o.Validate()==nil{t.Fatal("invalid trusted peer accepted")}
 o.TrustedPeers=[]string{"127.0.0.1/32"};o.TenantHeader="Authorization";if o.Validate()==nil{t.Fatal("authentication header reused as tenant")}
 o.TenantHeader="X-Tenant";o.StorePath="responses.sqlite";if o.Validate()==nil{t.Fatal("persistent store without encryption key configured")}
}
