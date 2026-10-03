package server

import (
 "encoding/json"
 "net/http"
 "os"
 "path/filepath"
 "testing"
 "time"

 "github.com/oai-prism/oaiprism/internal/catalog"
 "github.com/oai-prism/oaiprism/internal/config"
)

func TestCatalogRoutesOnlyToDeclaredAccount(t *testing.T){
 file:=filepath.Join(t.TempDir(),"models.json")
 raw,_:=json.Marshal(catalog.Snapshot{ObservedAt:time.Now(),Models:[]catalog.Model{{ID:"discovered",UpstreamID:"instant-model",ReasoningEfforts:[]string{"medium","ultra"},DefaultEffort:"medium",ContextWindow:65536}}})
 if err:=os.WriteFile(file,raw,0600);err!=nil{t.Fatal(err)}
 fake:=&fakeUpstream{t:t};base:=fake.handler()
 upstream:=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.URL.Path=="/api/llm/response_with_tools_start"&&r.Header.Get("Authorization")!="Bearer good-catalog-token"{t.Errorf("used non-eligible account: %s",r.Header.Get("Authorization"))}
  base.ServeHTTP(w,r)
 })
 ts,_:=gatewayE2E(t,upstream,func(c *config.Config){
  c.Creds.Accounts=append(c.Creds.Accounts,config.AccountConfig{ID:"catalog-only",AccessToken:"good-catalog-token",MaxConcurrency:1})
  c.Facade.Gateway.Catalog=catalog.Options{Sources:[]catalog.Source{{Name:"declared",AccountID:"catalog-only",Path:file}},Publish:[]string{"discovered"}}
 })
 response,body:=gatewayPost(t,ts.URL+"/v1/responses",`{"model":"discovered","input":"x","reasoning":{"effort":"ultra"}}`)
 if response.StatusCode!=200{t.Fatalf("%d %s",response.StatusCode,body)}
 fake.mu.Lock();defer fake.mu.Unlock()
 if len(fake.startBodies)!=1{t.Fatal("unexpected upstream attempts",len(fake.startBodies))}
 metadata,ok:=fake.startBodies[0]["metadata"].(map[string]any)
 if !ok||metadata["model"]!="instant-model"||metadata["reasoning_effort"]!="ultra"{t.Fatalf("model/effort changed: %+v",fake.startBodies)}
}
func TestMissingCatalogCannotFallBackToStaticAccount(t *testing.T){
 fake:=&fakeUpstream{t:t}
 ts,_:=gatewayE2E(t,fake.handler(),func(c *config.Config){
  c.Facade.Gateway.Catalog=catalog.Options{Sources:[]catalog.Source{{Name:"missing",AccountID:"main",Path:filepath.Join(t.TempDir(),"missing.json")}},Publish:[]string{"instant"}}
 })
 response,body:=gatewayPost(t,ts.URL+"/v1/responses",`{"model":"instant","input":"x"}`)
 if response.StatusCode!=503{t.Fatalf("missing declaration silently fell back: %d %s",response.StatusCode,body)}
 fake.mu.Lock();defer fake.mu.Unlock();if len(fake.startBodies)!=0{t.Fatal("request bypassed capability routing")}
}
