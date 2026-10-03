package catalog

import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "sync"
 "testing"
 "time"
)
func snapshot(t *testing.T,path string,models []Model){t.Helper();raw,err:=json.Marshal(Snapshot{ObservedAt:time.Now(),Models:models});if err!=nil{t.Fatal(err)};if err=os.WriteFile(path,raw,0600);err!=nil{t.Fatal(err)}}
func example(efforts ...string)Model{return Model{ID:"candidate",UpstreamID:"real-model",ReasoningEfforts:efforts,DefaultEffort:efforts[0],ContextWindow:65536,InputModalities:[]string{"text"}}}
func TestRefreshPreservesLastGoodAndExpires(t *testing.T){
 path:=filepath.Join(t.TempDir(),"catalog.json");snapshot(t,path,[]Model{example("medium")})
 r,err:=New(Options{Sources:[]Source{{Name:"source",AccountID:"account-a",Path:path}},Publish:[]string{"candidate"}});if err!=nil{t.Fatal(err)};defer r.Close()
 if _,accounts,err:=r.Select("candidate","medium");err!=nil||len(accounts)!=1||accounts[0]!="account-a"{t.Fatal(accounts,err)}
 if err=os.WriteFile(path,[]byte(`{"broken"`),0600);err!=nil{t.Fatal(err)};r.Refresh(context.Background())
 if _,accounts,err:=r.Select("candidate","medium");err!=nil||len(accounts)!=1{t.Fatal("transient refresh failure lost last good",err)}
 r.mu.Lock();s:=r.states["source"];s.snapshot.ObservedAt=time.Now().Add(-2*time.Hour);r.states["source"]=s;r.mu.Unlock()
 if _,_,err=r.Select("candidate","medium");err==nil{t.Fatal("stale capability used")}
 if records:=r.Records();len(records)!=1||!records[0].Stale{t.Fatal("lost stale diagnostic state")}
}
func TestAccountSpecificEffortsAndImmutableSnapshots(t *testing.T){
 a,b:=filepath.Join(t.TempDir(),"a.json"),filepath.Join(t.TempDir(),"b.json")
 snapshot(t,a,[]Model{example("medium","high")});snapshot(t,b,[]Model{example("medium","ultra")})
 r,err:=New(Options{Sources:[]Source{{Name:"a",AccountID:"acct-a",Path:a},{Name:"b",AccountID:"acct-b",Path:b}}});if err!=nil{t.Fatal(err)};defer r.Close()
 _,accounts,err:=r.Select("candidate","ultra");if err!=nil||len(accounts)!=1||accounts[0]!="acct-b"{t.Fatalf("wrong effort routed %+v %v",accounts,err)}
 records:=r.Records();records[0].ReasoningEfforts[0]="mutated";records[0].Capabilities["shell"]=true
 if r.Records()[0].ReasoningEfforts[0]=="mutated"||r.Records()[0].Capabilities["shell"]{t.Fatal("caller mutated internal snapshot")}
 if r.Published("candidate"){t.Fatal("discovery automatically published model")}
 var wg sync.WaitGroup;for i:=0;i<8;i++{wg.Add(1);go func(){defer wg.Done();for j:=0;j<10;j++{r.Records();_,_,_=r.Select("candidate","high")}}()};wg.Wait()
}
func TestCatalogHTTPNoRedirectAndCredentialRedaction(t *testing.T){
 var credential string
 destination:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){credential=r.Header.Get("Authorization");t.Error("followed redirect")}));defer destination.Close()
 source:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){if r.Header.Get("Authorization")!="Bearer test-only-token"{t.Error("missing source credential")};http.Redirect(w,r,destination.URL,302)}));defer source.Close()
 t.Setenv("CATALOG_TEST_KEY","test-only-token")
 registry,err:=New(Options{Sources:[]Source{{Name:"s",AccountID:"a",URL:source.URL,BearerEnv:"CATALOG_TEST_KEY"}}});if err!=nil{t.Fatal(err)};defer registry.Close()
 if credential!=""||len(registry.Records())!=0{t.Fatal("redirect leaked token or fabricated records")}
}
func TestCatalogRejectsUntrustedShapes(t *testing.T){
 for _,source:=range []Source{
  {Name:"s",AccountID:"a",URL:"http://example.com/models"},
  {Name:"s",AccountID:"a",URL:"https://user:password@example.com/models"},
  {Name:"s",AccountID:"a",Path:"x",URL:"https://example.com"},
  {Name:"s",Path:"x"},
 }{if (Options{Sources:[]Source{source}}).Validate()==nil{t.Fatalf("accepted unsafe source %+v",source)}}
 now:=time.Now();base:=Snapshot{ObservedAt:now,Models:[]Model{example("medium")}}
 raw,_:=json.Marshal(base)
 if _,err:=Decode(append(raw,[]byte(" {}")...),now,time.Hour);err==nil{t.Fatal("trailing snapshot accepted")}
 base.Models=append(base.Models,base.Models[0]);raw,_=json.Marshal(base);if _,err:=Decode(raw,now,time.Hour);err==nil{t.Fatal("duplicate model accepted")}
 base.Models=[]Model{example("medium")};base.ObservedAt=now.Add(-2*time.Hour);raw,_=json.Marshal(base);if _,err:=Decode(raw,now,time.Hour);err==nil{t.Fatal("stale snapshot accepted")}
 if _,err:=Decode([]byte(strings.Repeat(" ",MaxSnapshotBytes+1)),now,time.Hour);err==nil{t.Fatal("unbounded snapshot")}
}
