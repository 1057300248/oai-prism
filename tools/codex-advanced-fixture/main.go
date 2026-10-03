// Local deterministic test engine, not a production model or quality evaluator.
package main

import (
 "context"
 "crypto/sha256"
 "encoding/base64"
 "encoding/hex"
 "encoding/json"
 "errors"
 "fmt"
 "log"
 "net/http"
 "os"
 "path/filepath"
 "regexp"
 "strconv"
 "strings"
 "sync"
 "time"

 "github.com/oai-prism/oaiprism/internal/attachment"
 "github.com/oai-prism/oaiprism/internal/catalog"
 "github.com/oai-prism/oaiprism/internal/gateway"
)
var models=[]string{"advanced-image","advanced-view","advanced-mcp","advanced-stdin","advanced-parallel","advanced-files"}
type fixture struct{mu sync.Mutex;report,work string}
func(f *fixture)record(v any){f.mu.Lock();defer f.mu.Unlock();out,err:=os.OpenFile(f.report,os.O_WRONLY|os.O_APPEND|os.O_CREATE,0600);if err!=nil{panic(err)};defer out.Close();_ = json.NewEncoder(out).Encode(v)}
func(f *fixture)Run(ctx context.Context,q *gateway.Request,accepted func()error,emit func(gateway.Delta)error)(*gateway.Result,error){
 if err:=ctx.Err();err!=nil{return nil,err}
 outputs:=[]gateway.Item{};hashes:=[]string{};userText:=""
 for _,it:=range q.Items{
  if it.Type=="function_call_output"||it.Type=="custom_tool_call_output"{outputs=append(outputs,it)}
  for _,part:=range it.Content{
   if it.Role=="user"{userText+=part.Text+"\n"}
   if part.Type=="input_image"{_,b,ok:=strings.Cut(part.ImageURL,",");if !ok{return nil,errors.New("missing inline image bytes")};data,err:=base64.StdEncoding.DecodeString(b);if err!=nil{return nil,err};sum:=sha256.Sum256(data);hashes=append(hashes,hex.EncodeToString(sum[:]))}
  }
 }
 text:="";calls:=[]any{};toolNames:=[]string{}
 for _,t:=range q.Tools{toolNames=append(toolNames,t.Namespace+"."+t.Name)}
 call:=func(name string,args any)error{for _,t:=range q.Tools{if t.Name==name||strings.Contains(t.Name,name){c:=map[string]any{"name":t.Name};if t.Namespace!=""{c["namespace"]=t.Namespace};if t.Type=="custom"{c["input"]=args}else{c["arguments"]=args};calls=append(calls,c);return nil}};return fmt.Errorf("fixture tool %s not declared; names=%v",name,toolNames)}
 last:="";if len(outputs)>0{b,_:=json.Marshal(outputs[len(outputs)-1]);last=string(b)}
 var err error
 switch q.Model{
 case "advanced-image":
  if len(hashes)==0{return nil,errors.New("CLI image did not reach gateway")};text="IMAGE_BYTES_RECEIVED"
 case "advanced-view":
  if len(outputs)==0{err=call("view_image",map[string]any{"path":filepath.Join(f.work,"image.png")})}else{if len(hashes)==0{return nil,errors.New("view_image output lost its pixels")};text="VIEW_IMAGE_RECEIVED"}
 case "advanced-mcp":
  if len(outputs)==0{err=call("fixture_echo",map[string]any{"text":"MCP_ROUND_TRIP"})}else{if !strings.Contains(last,"MCP_ROUND_TRIP")||len(hashes)==0{return nil,fmt.Errorf("MCP text/image lost: %s",last)};text="MCP_IMAGE_AND_TEXT_RECEIVED"}
 case "advanced-stdin":
  if len(outputs)==0{err=call("exec_command",map[string]any{"cmd":"python3 -u -c \"print('READY', flush=True); value=input(); print('ECHO:'+value, flush=True)\"","tty":true,"yield_time_ms":1000,"max_output_tokens":2000,"sandbox_permissions":"use_default"})}else if len(outputs)==1{
   re:=regexp.MustCompile(`(?i)session(?:_id| ID| id)?[\\\" :]+([0-9]+)`);match:=re.FindStringSubmatch(last);if len(match)<2{return nil,fmt.Errorf("no process session ID in: %s",last)};id,_:=strconv.Atoi(match[1]);err=call("write_stdin",map[string]any{"session_id":id,"chars":"hello-fixture\n","yield_time_ms":1000,"max_output_tokens":2000})
  }else{if !strings.Contains(last,"ECHO:hello-fixture"){return nil,fmt.Errorf("stdin continuation failed: %s",last)};text="INTERACTIVE_PROCESS_COMPLETED"}
 case "advanced-parallel":
  if strings.Contains(userText,"CHILD_FIXTURE_CHECK_739"){text="CHILD_FIXTURE_DONE_739";break}
  if len(outputs)==0{err=call("spawn_agent",map[string]any{"message":"CHILD_FIXTURE_CHECK_739: Return CHILD_FIXTURE_DONE_739 without tools.","agent_type":"default","fork_context":false})}else if len(outputs)==1{
   re:=regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`);id:=re.FindString(last);if id==""{return nil,fmt.Errorf("no child ID: %s",last)};err=call("wait",map[string]any{"ids":[]string{id},"timeout_ms":10000})
  }else{if !strings.Contains(last,"CHILD_FIXTURE_DONE_739"){return nil,fmt.Errorf("child result not received: %s",last)};text="SUBAGENT_ROUND_TRIP_COMPLETED"}
 case "advanced-files":
  text="FILE_CONTENT_RECEIVED";if !strings.Contains(userText,"SDK_FILE_CONTENT_513")&&len(hashes)==0{return nil,errors.New("expected uploaded input content")}
 default:return nil,errors.New("unknown advanced test model")
 }
 record:=map[string]any{"model":q.Model,"prior_outputs":len(outputs),"image_hashes":hashes,"declared_tools":toolNames,"returned_calls":calls,"received_outputs":outputs,"text":text}
 if err!=nil{record["error"]=err.Error()};f.record(record)
 if err!=nil{return nil,err}
 raw,_:=json.Marshal(map[string]any{"text":text,"tool_calls":calls})
 answer:=text;if len(q.Tools)>0{answer=string(raw)}
 if accepted!=nil{if err:=accepted();err!=nil{return nil,err}}
 if emit!=nil{if err:=emit(gateway.Delta{Text:answer});err!=nil{return nil,err}}
 return &gateway.Result{Text:answer,Usage:&gateway.Usage{Input:100,Output:20,Source:"upstream"}},nil
}
func main(){
 work,report:=os.Getenv("CODEX_FIXTURE_WORKDIR"),os.Getenv("CODEX_FIXTURE_REPORT");if work==""||report==""{log.Fatal("isolated fixture work/report required")}
 dir,err:=os.MkdirTemp("","codex-media-catalog-");if err!=nil{log.Fatal(err)};defer os.RemoveAll(dir)
 entries:=[]catalog.Model{};for _,id:=range models{entries=append(entries,catalog.Model{ID:id,UpstreamID:id,DisplayName:id,ReasoningEfforts:[]string{"medium"},DefaultEffort:"medium",ContextWindow:65536,InputModalities:[]string{"text","image"}})}
 raw,_:=json.Marshal(catalog.Snapshot{ObservedAt:time.Now(),Models:entries});path:=filepath.Join(dir,"models.json");if err=os.WriteFile(path,raw,0600);err!=nil{log.Fatal(err)}
 h,err:=gateway.New(gateway.Options{Enabled:true,APIKeys:[]string{"advanced-fixture-key"},Models:map[string]string{},PromptTools:true,CodexTools:true,StructuredOutput:true,LocalOutputLimit:true,InlineImages:true,Files:attachment.Options{Enabled:true},TenantHeader:"X-Fixture-Tenant",TrustedPeers:[]string{"127.0.0.1/32"},Media:gateway.MediaPolicy{ImageReserve:2048},ResponseStore:true,Timeout:60*time.Second,Catalog:catalog.Options{Sources:[]catalog.Source{{Name:"fixture",AccountID:"local-fixture",Path:path}},Publish:models}},&fixture{report:report,work:work})
 if err!=nil{log.Fatal(err)};defer h.Close()
 srv:=&http.Server{Addr:"127.0.0.1:18792",Handler:h,ReadHeaderTimeout:5*time.Second,ReadTimeout:30*time.Second,IdleTimeout:30*time.Second}
 log.Print("LOCAL DETERMINISTIC ADVANCED FIXTURE:127.0.0.1:18792")
 log.Fatal(srv.ListenAndServe())
}
