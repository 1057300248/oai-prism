"""Guarded corrections to test clients; never relax gateway validation."""
from pathlib import Path
files={}
def edit(path,old,new):
    text=files.get(path,Path(path).read_text())
    assert text.count(old)==1,(path,text.count(old),old[:100])
    files[path]=text.replace(old,new)
p='tools/codex_advanced_test.py'
edit(p,"*flags, prompt]","*flags, '--', prompt]")
edit(p,"with client.with_options(default_headers={'X-Fixture-Tenant': 'other-owner'}) as other:","with OpenAI(api_key=KEY, base_url=BASE, default_headers={'X-Fixture-Tenant': 'other-owner'}, max_retries=0, timeout=15) as other:")
p='tools/codex-advanced-fixture/main.go'
edit(p,' text:="";calls:=[]any{};toolNames:=[]string{}',''' child:=false
 for _,it:=range q.Items{if it.Role=="user"{for _,part:=range it.Content{child=child||strings.HasPrefix(part.Text,"CHILD_FIXTURE_CHECK_739:")}}}
 properties:=func(name string)map[string]json.RawMessage{for _,t:=range q.Tools{if strings.Contains(t.Name,name){var schema struct{Properties map[string]json.RawMessage `json:"properties"`};_ = json.Unmarshal(t.Parameters,&schema);return schema.Properties}};return nil}
 text:="";calls:=[]any{};toolNames:=[]string{}''')
edit(p,'if strings.Contains(userText,"CHILD_FIXTURE_CHECK_739"){text="CHILD_FIXTURE_DONE_739";break}', 'if child{text="CHILD_FIXTURE_DONE_739";break}\n  if len(outputs)>0&&strings.Contains(userText,"CHILD_FIXTURE_DONE_739"){text="SUBAGENT_ROUND_TRIP_COMPLETED";break}')
edit(p,'if len(outputs)==0{err=call("spawn_agent",map[string]any{"message":"CHILD_FIXTURE_CHECK_739: Return CHILD_FIXTURE_DONE_739 without tools.","agent_type":"default","fork_context":false})}else if len(outputs)==1{', '''if len(outputs)==0{
   props:=properties("spawn_agent");args:=map[string]any{"message":"CHILD_FIXTURE_CHECK_739: Return CHILD_FIXTURE_DONE_739 without tools."}
   if props["task_name"]!=nil{args["task_name"]="fixture_child"};if props["fork_context"]!=nil{args["fork_context"]=false};if props["fork_turns"]!=nil{args["fork_turns"]="none"}
   err=call("spawn_agent",args)
  }else if len(outputs)==1{''')
edit(p,'re:=regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`);id:=re.FindString(last);if id==""{return nil,fmt.Errorf("no child ID: %s",last)};err=call("wait",map[string]any{"ids":[]string{id},"timeout_ms":10000})', '''props:=properties("wait");args:=map[string]any{"timeout_ms":10000}
   if props["targets"]!=nil||props["ids"]!=nil{
    re:=regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`);id:=re.FindString(last);if id==""{return nil,fmt.Errorf("no child ID: %s",last)}
    if props["targets"]!=nil{args["targets"]=[]string{id}}else{args["ids"]=[]string{id}}
   }
   err=call("wait",args)''')
# Include fixture-only contract diagnostics so a test error is not mistaken for
# an upstream production failure. No production handlers reveal internal details.
edit(p,'if err!=nil{record["error"]=err.Error()};f.record(record)', '''if err!=nil{record["error"]=err.Error()};if q.Model=="advanced-parallel"{record["tool_schemas"]=q.Tools};f.record(record)''')
for p,s in files.items():Path(p).write_text(s)
print('Updated',len(files),'test fixtures')
