package gateway

import (
 "context"
 "encoding/json"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"

 "github.com/oai-prism/oaiprism/internal/catalog"
)

func codexOptions()Options{o:=options();o.CodexTools=true;return o}
func TestCodexNamespaceAndCustomCallRoundtrip(t *testing.T){
 body:=`{"model":"test-model","input":"edit","tools":[{"type":"namespace","name":"local","description":"Caller tools","tools":[{"type":"function","name":"read","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}},{"type":"custom","name":"patch","format":{"type":"text"}}]}],"reasoning":{"effort":"high","summary":"auto"},"include":["reasoning.encrypted_content"],"client_metadata":{"session_id":"untrusted-routing-label"}}`
 q,err:=Parse([]byte(body),true,codexOptions());if err!=nil{t.Fatal(err)}
 if q.ReasoningSummary!="auto"||len(q.Tools)!=2||q.Tools[1].Namespace!="local"{t.Fatalf("lost metadata %+v",q)}
 contract,err:=Prepare(q);if err!=nil{t.Fatal(err)}
 result:=&Result{Text:`{"text":"","tool_calls":[{"name":"patch","namespace":"local","input":"PATCH CONTENT"}]}`}
 if err=contract.Finalize(q,result);err!=nil{t.Fatal(err)}
 if result.Calls[0].Type!="custom_tool_call"||result.Calls[0].Input!="PATCH CONTENT"{t.Fatal(result.Calls)}
 item:=result.Calls[0];item.CallID="call_real";item.ID="tool_item"
 encoded,_:=json.Marshal([]any{toolCallJSON(item),map[string]any{"type":"custom_tool_call_output","call_id":"call_real","output":[]any{map[string]any{"type":"input_text","text":"applied"}}},map[string]any{"type":"message","role":"user","content":"continue"}})
 parsed,err:=parseCodexItems(encoded,codexOptions());if err!=nil{t.Fatal(err)}
 if err=ValidateHistory(parsed);err!=nil{t.Fatal(err)}
 if parsed[1].Content[0].Text!="applied"{t.Fatal("tool result blocks lost")}
 parsed[1].Type="function_call_output";if ValidateHistory(parsed)==nil{t.Fatal("mismatched custom/function result accepted")}
}
func TestCustomGrammarValidationAndUndeclaredNames(t *testing.T){
 o:=codexOptions()
 body:=`{"model":"test-model","input":"x","tools":[{"type":"custom","name":"code","format":{"type":"grammar","syntax":"regex","definition":"[a-z]+"}}]}`
 q,err:=Parse([]byte(body),true,o);if err!=nil{t.Fatal(err)};contract,err:=Prepare(q);if err!=nil{t.Fatal(err)}
 for _,text:=range []string{`{"text":"","tool_calls":[{"name":"unknown","input":"abc"}]}`,`{"text":"","tool_calls":[{"name":"code","input":"123"}]}`,`{"text":"","tool_calls":[{"name":"code","arguments":{}}]}`,`{"text":"","tool_calls":[{"name":"code","namespace":"wrong","input":"abc"}]}`} {
  if contract.Finalize(q,&Result{Text:text})==nil{t.Fatal("invalid custom invocation accepted",text)}
 }
 if contract.Finalize(q,&Result{Text:`{"text":"","tool_calls":[{"name":"code","input":"abc"}]}`})!=nil{t.Fatal("valid regex input rejected")}
 if _,err=Parse([]byte(strings.Replace(body,`"regex"`,`"lark"`,1)),true,o);err==nil{t.Fatal("unknown grammar advertised as supported")}
 if _,err=Parse([]byte(body),true,options());err==nil{t.Fatal("custom bridge enabled implicitly")}
}
func TestCodexPreservesConstraintsAndRejectsOpaqueReasoning(t *testing.T){
 raw:=`[{"type":"message","role":"developer","content":"Never modify protected files."},{"type":"message","role":"assistant","phase":"commentary","content":"I will inspect the file."},{"type":"reasoning","id":"rs_old","summary":[{"type":"summary_text","text":"Earlier public summary"}]},{"type":"message","role":"user","content":"continue"}]`
 items,err:=parseCodexItems([]byte(raw),codexOptions());if err!=nil{t.Fatal(err)}
 render,_:=json.Marshal(RenderInput(&Request{Items:items}))
 if !strings.Contains(string(render),"Never modify protected files")||!strings.Contains(string(render),"Earlier public summary")||!strings.Contains(string(render),"commentary"){t.Fatal("constraints or history dropped",string(render))}
 bad:=strings.Replace(raw,`"summary":[`, `"encrypted_content":"opaque-secret","summary":[`,1)
 if _,err=parseCodexItems([]byte(bad),codexOptions());err==nil{t.Fatal("encrypted upstream state guessed/decrypted")}
}
func TestCodexCustomSSEAndNoServerExecution(t *testing.T){
 h:=harness(t,func(ctx context.Context,q *Request,a func()error,e func(Delta)error)(*Result,error){if err:=a();err!=nil{return nil,err};return &Result{Text:`{"text":"","tool_calls":[{"name":"patch","input":"fixed input"}]}`},nil},func(o *Options){o.CodexTools=true})
 w:=call(h,"POST","/v1/responses",`{"model":"test-model","input":"x","stream":true,"tools":[{"type":"custom","name":"patch","format":{"type":"text"}}]}`,"key-a","")
 if w.Code!=200||!strings.Contains(w.Body.String(),"response.custom_tool_call_input.delta")||!strings.Contains(w.Body.String(),`"type":"custom_tool_call"`){t.Fatal(w.Code,w.Body.String())}
 ev:=events(t,w.Body.String());final:=ev[len(ev)-1]["response"].(map[string]any)
 if final["tools"].([]any)[0].(map[string]any)["type"]!="custom"{t.Fatal("custom declaration changed type")}
}
func TestCatalogPublishedEffortsAndPrivacy(t *testing.T){
 path:=filepath.Join(t.TempDir(),"models.json")
 raw,_:=json.Marshal(catalog.Snapshot{ObservedAt:time.Now(),Models:[]catalog.Model{{ID:"new-model",UpstreamID:"private-upstream",DisplayName:"New model",ReasoningEfforts:[]string{"medium","ultra"},DefaultEffort:"medium",ContextWindow:65536},{ID:"not-published",UpstreamID:"secret-candidate"}}})
 if err:=os.WriteFile(path,raw,0600);err!=nil{t.Fatal(err)}
 h:=harness(t,func(ctx context.Context,q *Request,a func()error,e func(Delta)error)(*Result,error){if q.ResolvedModel!="private-upstream"||len(q.AllowedAccounts)!=1||q.AllowedAccounts[0]!="private-account"||q.Effort!="ultra"{t.Fatalf("wrong selected account/effort %+v",q)};return &Result{Text:"ok"},nil},func(o *Options){o.CodexTools=true;o.Catalog=catalog.Options{Sources:[]catalog.Source{{Name:"private-source",AccountID:"private-account",Path:path}},Publish:[]string{"new-model"}}})
 models:=call(h,"GET","/v1/models","","key-a","")
 for _,secret:=range []string{"private-account","private-source","private-upstream","not-published","secret-candidate"}{if strings.Contains(models.Body.String(),secret){t.Fatal("metadata leak",secret)}}
 w:=call(h,"POST","/v1/responses",`{"model":"new-model","input":"x","reasoning":{"effort":"ultra"}}`,"key-a","");if w.Code!=200{t.Fatal(w.Code,w.Body.String())}
 w=call(h,"POST","/v1/responses",`{"model":"new-model","input":"x","reasoning":{"effort":"low"}}`,"key-a","");if w.Code!=400{t.Fatal("unsupported effort not rejected",w.Code)}
 w=call(h,"GET","/v1/codex/models","","key-a","");body:=readMap(t,w);model:=body["models"].([]any)[0].(map[string]any)
 if model["shell_type"]!="unified_exec"||model["apply_patch_tool_type"]!="freeform"||model["use_responses_lite"]!=false||model["node_repl_disabled"]!=true{t.Fatal("unsafe/incorrect client defaults",model)}
}
