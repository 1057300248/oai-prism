// Deterministic localhost-only protocol fixture. NEVER a production backend.
package main

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "log"
 "net/http"
 "os"
 "path/filepath"
 "strings"
 "sync"
 "time"

 "github.com/oai-prism/oaiprism/internal/catalog"
 "github.com/oai-prism/oaiprism/internal/gateway"
)

type fixture struct{mu sync.Mutex;report string;work string}
func(f *fixture)record(value any){f.mu.Lock();defer f.mu.Unlock();file,err:=os.OpenFile(f.report,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{panic(err)};defer file.Close();_ = json.NewEncoder(file).Encode(value)}
func(f *fixture)Run(ctx context.Context,q *gateway.Request,accepted func()error,emit func(gateway.Delta)error)(result *gateway.Result,runErr error){
 defer func(){if runErr!=nil{log.Printf("FIXTURE engine error: %v",runErr)}}()
 if ctx.Err()!=nil{return nil,ctx.Err()}
 if q.InternalSummary{return nil,errors.New("fixture did not authorize summary calls")}
 outputs:=[]gateway.Item{}
 for _,item:=range q.Items{if item.Type=="function_call_output"||item.Type=="custom_tool_call_output"{outputs=append(outputs,item)}}
 diagnostics,_:=json.Marshal(map[string]any{"model":q.Model,"received_outputs":outputs,"declared_tools":q.Tools})
 log.Printf("FIXTURE request: %s",diagnostics)
 if len(outputs)>0{
  last:=outputs[len(outputs)-1];data,_:=json.Marshal(last)
  for _,marker:=range []string{"unsupported custom tool","failed to parse","execution error","Permission denied","Process exited with code 1","patch rejected"}{if strings.Contains(string(data),marker){return nil,fmt.Errorf("client tool execution failed: %s",marker)}}
 }
 tools:=map[string]gateway.Tool{};for _,tool:=range q.Tools{tools[tool.Name]=tool}
 text:="";calls:=[]any{}
 command:=func(name string,args any)error{tool,ok:=tools[name];if !ok{return fmt.Errorf("required fixture tool not declared: %s",name)};call:=map[string]any{"name":tool.Name,"namespace":tool.Namespace};if tool.Type=="custom"{call["input"]=args}else{call["arguments"]=args};calls=append(calls,call);return nil}
 var err error
 switch q.Model{
 case "codex-fixture":
  switch len(outputs){
  case 0:err=command("exec_command",map[string]any{"cmd":"python3 -c \"from pathlib import Path; print(Path('calc.py').read_text())\"","max_output_tokens":2000,"sandbox_permissions":"use_default"})
  case 1:err=command("apply_patch","*** Begin Patch\n*** Update File: calc.py\n@@\n def add(a, b):\n-    return a - b\n+    return a + b\n*** End Patch\n")
  case 2:err=command("exec_command",map[string]any{"cmd":"python3 -m unittest -v","max_output_tokens":2000,"sandbox_permissions":"use_default"})
  case 3:text="Fixture task completed: calc.py fixed and the client tests passed."
  default:return nil,errors.New("unexpected additional fixture tool turn")
  }
 case "codex-review":
  if len(outputs)==0{err=command("exec_command",map[string]any{"cmd":"git diff -- calc.py && python3 -c \"from pathlib import Path; print(Path('calc.py').read_text())\"","max_output_tokens":2000,"sandbox_permissions":"use_default"})}else{
   review:=map[string]any{"findings":[]any{map[string]any{"title":"[P1] Addition must not subtract its second operand","body":"The changed return expression computes a-b, so add(2,3) returns -1 instead of 5. This is a deterministic fixture finding, not a live model assessment.","confidence_score":1.0,"priority":1,"code_location":map[string]any{"absolute_file_path":filepath.Join(f.work,"calc.py"),"line_range":map[string]any{"start":2,"end":2}}}},"overall_correctness":"patch is incorrect","overall_explanation":"The introduced subtraction fails the addition contract.","overall_confidence_score":1.0}
   raw,_:=json.Marshal(review);text=string(raw)
  }
 default:return nil,errors.New("unknown fixture scenario")
 }
 if err!=nil{return nil,err}
 f.record(map[string]any{"model":q.Model,"effort":q.Effort,"prior_outputs":len(outputs),"declared_tools":len(q.Tools),"returned_calls":calls,"received_outputs":outputs})
 envelope,_:=json.Marshal(map[string]any{"text":text,"tool_calls":calls})
 if accepted!=nil{if err=accepted();err!=nil{return nil,err}}
 if emit!=nil{if err=emit(gateway.Delta{Text:string(envelope)});err!=nil{return nil,err}}
 return &gateway.Result{Text:string(envelope),Usage:&gateway.Usage{Input:50,Output:20,Source:"upstream"}},nil
}
func main(){
 work:=os.Getenv("CODEX_FIXTURE_WORKDIR");report:=os.Getenv("CODEX_FIXTURE_REPORT")
 if work==""||report==""{log.Fatal("fixture workdir/report required")}
 dir,err:=os.MkdirTemp("","codex-fixture-catalog-");if err!=nil{log.Fatal(err)};defer os.RemoveAll(dir)
 path:=filepath.Join(dir,"models.json")
 models:=[]catalog.Model{}
 for _,id:=range []string{"codex-fixture","codex-review"}{models=append(models,catalog.Model{ID:id,UpstreamID:id,DisplayName:id,ReasoningEfforts:[]string{"low","medium","high","max"},DefaultEffort:"medium",ContextWindow:65536,InputModalities:[]string{"text"}})}
 raw,_:=json.Marshal(catalog.Snapshot{ObservedAt:time.Now(),Models:models});if err=os.WriteFile(path,raw,0600);err!=nil{log.Fatal(err)}
 h,err:=gateway.New(gateway.Options{Enabled:true,APIKeys:[]string{"codex-fixture-key"},Models:map[string]string{},PromptTools:true,CodexTools:true,StructuredOutput:true,LocalOutputLimit:true,Timeout:30*time.Second,Catalog:catalog.Options{Sources:[]catalog.Source{{Name:"fixture",AccountID:"local-fixture",Path:path}},Publish:[]string{"codex-fixture","codex-review"}}},&fixture{report:report,work:work})
 if err!=nil{log.Fatal(err)};defer h.Close()
 server:=&http.Server{Addr:"127.0.0.1:18790",Handler:h,ReadHeaderTimeout:5*time.Second,ReadTimeout:15*time.Second,IdleTimeout:30*time.Second}
 log.Print("LOCAL DETERMINISTIC CODEX FIXTURE ONLY on 127.0.0.1:18790")
 log.Fatal(server.ListenAndServe())
}
