package server

import (
 "bufio"
 "context"
 "io"
 "log/slog"
 "net/http"
 "net/http/httptest"
 "path/filepath"
 "strings"
 "sync/atomic"
 "testing"
 "time"

 "github.com/oai-prism/oaiprism/internal/config"
)

// Complete in-process HTTP chain, with a local fake Prism upstream only.
func gatewayE2E(t *testing.T,upstream http.Handler,tune func(*config.Config))(*httptest.Server,*Server){
 t.Helper();up:=httptest.NewServer(upstream);t.Cleanup(up.Close)
 cfg:=config.Default();cfg.Upstream.BaseURL=up.URL;cfg.Upstream.MaxRetries=0;cfg.Upstream.ForceHTTP2=false
 cfg.Creds.Mode="static";cfg.Creds.File=filepath.Join(t.TempDir(),"accounts.json");cfg.Creds.AutoRefresh=false
 cfg.Creds.Accounts=[]config.AccountConfig{{ID:"main",AccessToken:"good-token",MaxConcurrency:1}}
 cfg.Pool.HealthCheck=false;cfg.Pool.Cooldown=time.Second
 cfg.Facade.Gateway.Enabled=true;cfg.Facade.Gateway.Timeout=2*time.Second
 cfg.Facade.APIKeys=[]string{"channel-test-key"};cfg.Facade.DefaultModel="test-model"
 cfg.Facade.Models=map[string]config.ModelMapping{"test-model":{Model:"gpt-5"},"instant":{Model:"instant-model"},"failing":{Model:"failing-model"}}
 cfg.Facade.DefaultSystemPrompt="";cfg.Facade.UseSandbox=false;cfg.Facade.UseStatusWait=false
 cfg.Facade.PollInterval=5*time.Millisecond;cfg.Facade.PollBackoffMax=10*time.Millisecond;cfg.Facade.MaxPollTimeout=2*time.Second;cfg.Facade.SyncTimeout=2*time.Second
 cfg.Capture.Enabled=false;if tune!=nil{tune(cfg)}
 srv,err:=New(cfg,slog.New(slog.NewTextHandler(io.Discard,nil)));if err!=nil{t.Fatal(err)}
 ts:=httptest.NewServer(srv.Handler());t.Cleanup(func(){ts.Close();_ = srv.Close()});return ts,srv
}
func gatewayPost(t *testing.T,url,body string)(*http.Response,string){
 t.Helper();req,err:=http.NewRequest("POST",url,strings.NewReader(body));if err!=nil{t.Fatal(err)}
 req.Header.Set("Authorization","Bearer channel-test-key");req.Header.Set("Content-Type","application/json")
 resp,err:=(&http.Client{Timeout:5*time.Second}).Do(req);if err!=nil{t.Fatal(err)};defer resp.Body.Close();data,err:=io.ReadAll(resp.Body);if err!=nil{t.Fatal(err)};return resp,string(data)
}
func TestGatewayE2ETextRoutesAndFreshProjects(t *testing.T){
 fake:=&fakeUpstream{t:t};ts,_:=gatewayE2E(t,fake.handler(),nil)
 for _,path:=range []string{"/admin/accounts","/admin/oauth/status","/prism/api/echo"}{req,_:=http.NewRequest("GET",ts.URL+path,nil);req.Header.Set("Authorization","Bearer channel-test-key");resp,err:=http.DefaultClient.Do(req);if err!=nil{t.Fatal(err)};resp.Body.Close();if resp.StatusCode!=404{t.Fatalf("privileged route %s reachable: %d",path,resp.StatusCode)}}
 resp,body:=gatewayPost(t,ts.URL+"/v1/chat/completions",`{"model":"instant","messages":[{"role":"user","content":"hello"}]}`)
 if resp.StatusCode!=200||!strings.Contains(body,`"object":"chat.completion"`)||!strings.Contains(body,`"model":"instant"`){t.Fatalf("%d %s",resp.StatusCode,body)}
 resp,body=gatewayPost(t,ts.URL+"/v1/responses",`{"model":"test-model","input":"hello","stream":true,"store":false}`)
 if resp.StatusCode!=200||strings.Count(body,"event: response.completed\n")!=1{t.Fatalf("%d %s",resp.StatusCode,body)}
 fake.mu.Lock();defer fake.mu.Unlock();if fake.projectCount!=2||len(fake.startBodies)!=2{t.Fatalf("implicit project sharing: projects=%d starts=%d",fake.projectCount,len(fake.startBodies))}
 for _,b:=range fake.startBodies{if previous,ok:=b["previousResponseId"];ok&&previous!=nil&&previous!=""{t.Fatal("private upstream continuation leaked",previous)}}
}
func TestGatewayE2EInitialFailurePreservesHTTPStatus(t *testing.T){
 ts,_:=gatewayE2E(t,(&fakeUpstream{t:t}).handler(),nil)
 resp,body:=gatewayPost(t,ts.URL+"/v1/responses",`{"model":"failing","input":"x","stream":true}`)
 if resp.StatusCode!=502||strings.Contains(body,"event:")||strings.Contains(body,"User not found"){t.Fatalf("initial error misreported/leaked: %d %s",resp.StatusCode,body)}
}
func TestGatewayE2ESetupDeadline(t *testing.T){
 base:=(&fakeUpstream{t:t}).handler();cancelled:=make(chan struct{},1)
 up:=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){if r.URL.Path=="/api/projects"{<-r.Context().Done();cancelled<-struct{}{};return};base.ServeHTTP(w,r)})
 ts,_:=gatewayE2E(t,up,func(c *config.Config){c.Facade.Gateway.Timeout=80*time.Millisecond})
 resp,body:=gatewayPost(t,ts.URL+"/v1/responses",`{"model":"test-model","input":"x","stream":true}`)
 if resp.StatusCode!=504||strings.Contains(body,"event:"){t.Fatalf("setup not bounded: %d %s",resp.StatusCode,body)}
 select{case<-cancelled:case<-time.After(time.Second):t.Fatal("project creation context not cancelled")}
}
func TestGatewayE2ENoReplayAfterAcceptance(t *testing.T){
 fake:=&fakeUpstream{t:t};base:=fake.handler()
 up:=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){if r.URL.Path=="/api/llm/response_with_tools_status"{w.WriteHeader(401);_,_=w.Write([]byte(`{"error":"private-token"}`));return};base.ServeHTTP(w,r)})
 ts,_:=gatewayE2E(t,up,func(c *config.Config){c.Creds.Accounts=append(c.Creds.Accounts,config.AccountConfig{ID:"other",AccessToken:"good-other",MaxConcurrency:1})})
 resp,body:=gatewayPost(t,ts.URL+"/v1/responses",`{"model":"test-model","input":"x","stream":true}`)
 if resp.StatusCode!=200||!strings.Contains(body,"response.failed")||strings.Contains(body,"response.completed")||strings.Contains(body,"private-token"){t.Fatalf("%d %s",resp.StatusCode,body)}
 fake.mu.Lock();defer fake.mu.Unlock();if len(fake.startBodies)!=1{t.Fatal("accepted generation replayed",len(fake.startBodies))};if len(fake.stopBodies)!=1{t.Fatal("stop must run exactly once",len(fake.stopBodies))}
}
func TestGatewayE2EClientDisconnectStopsUpstream(t *testing.T){
 fake:=&fakeUpstream{t:t};base:=fake.handler();var stops atomic.Int32;stopped:=make(chan struct{},1)
 up:=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){base.ServeHTTP(w,r);if r.URL.Path=="/api/llm/response_with_tools_stop"{stops.Add(1);select{case stopped<-struct{}{}:default:}}})
 ts,_:=gatewayE2E(t,up,func(c *config.Config){c.Facade.PollInterval=50*time.Millisecond;c.Facade.PollBackoffMax=50*time.Millisecond})
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 req,_:=http.NewRequestWithContext(ctx,"POST",ts.URL+"/v1/responses",strings.NewReader(`{"model":"test-model","input":"x","stream":true}`));req.Header.Set("Authorization","Bearer channel-test-key");req.Header.Set("Content-Type","application/json")
 resp,err:=http.DefaultClient.Do(req);if err!=nil{t.Fatal(err)};scanner:=bufio.NewScanner(resp.Body);for scanner.Scan(){if strings.HasPrefix(scanner.Text(),"event: response.output_text.delta"){break}};cancel();resp.Body.Close()
 select{case<-stopped:case<-time.After(2*time.Second):t.Fatal("upstream generation continued after disconnect")};if stops.Load()!=1{t.Fatal("duplicate upstream stops",stops.Load())}
}
func TestAuditDoesNotTruncateLargeRequest(t *testing.T){
 ts,_:=newTestServer(t,&fakeUpstream{t:t},goodAccount(),func(c *config.Config){c.Facade.UseSandbox=false})
 payload:=`{"model":"instant-model","messages":[{"role":"user","content":"`+strings.Repeat("a",(1<<20)+100)+`"}]}`
 resp,err:=http.Post(ts.URL+"/v1/chat/completions","application/json",strings.NewReader(payload));if err!=nil{t.Fatal(err)};defer resp.Body.Close();body,_:=io.ReadAll(resp.Body)
 if resp.StatusCode!=200{t.Fatalf("audit truncated a valid >1 MiB body: %d %.200s",resp.StatusCode,body)}
}
