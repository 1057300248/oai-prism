package gateway

import (
 "bytes"
 "encoding/json"
 "strings"
)

func parseCodexItems(raw []byte,o Options)([]Item,error){
 var list []json.RawMessage;if err:=scalar(raw,&list,"input");err!=nil{return nil,err};if len(list)==0||len(list)>MaxItems{return nil,bad("input","Expected 1..1024 items.")}
 result:=[]Item{}
 for _,raw:=range list{
  fields,err:=Object(raw);if err!=nil{return nil,err};kind:="message"
  if v,ok:=fields["type"];ok{if err=scalar(v,&kind,"input.type");err!=nil{return nil,err}}
  if isToolCall(kind)||isToolOutput(kind){
   allowed:="type id call_id name namespace arguments input status";if isToolOutput(kind){allowed="type id call_id output"}
   if err=keys(fields,allowed);err!=nil{return nil,err};item:=Item{Type:kind}
   if err=scalar(fields["call_id"],&item.CallID,"call_id");err!=nil{return nil,err};if item.CallID==""||len(item.CallID)>128{return nil,bad("call_id","Invalid tool correlation ID.")}
   if id,ok:=fields["id"];ok&&!null(id){if err=scalar(id,&item.ID,"id");err!=nil{return nil,err}}
   if isToolCall(kind){
    if scalar(fields["name"],&item.Name,"name")!=nil||!functionName.MatchString(item.Name){return nil,bad("name","Invalid tool name.")}
    if ns,ok:=fields["namespace"];ok&&!null(ns){if scalar(ns,&item.Namespace,"namespace")!=nil||!functionName.MatchString(item.Namespace){return nil,bad("namespace","Invalid namespace.")}}
    if kind=="function_call"{if _,ok:=fields["input"];ok{return nil,bad("input","Function calls use arguments.")};if scalar(fields["arguments"],&item.Arguments,"arguments")!=nil||!json.Valid([]byte(item.Arguments)){return nil,bad("arguments","Function arguments must contain valid JSON.")}}else{if _,ok:=fields["arguments"];ok{return nil,bad("arguments","Custom calls use input.")};if err=scalar(fields["input"],&item.Input,"input");err!=nil{return nil,err}}
   }else{
    output:=bytes.TrimSpace(fields["output"])
    if len(output)>0&&output[0]=='"'{if err=scalar(output,&item.Output,"output");err!=nil{return nil,err}}else{
     var blocks []json.RawMessage;if err=scalar(output,&blocks,"output");err!=nil{return nil,err};if len(blocks)>128{return nil,bad("output","Too many tool result blocks.")}
     if len(blocks)>0{item.Content,err=parseContent(output,"user",o);if err!=nil{return nil,err}}
    }
   }
   result=append(result,item);continue
  }
  if kind=="reasoning"{
   if err=keys(fields,"type id summary encrypted_content status");err!=nil{return nil,err}
   if encrypted,ok:=fields["encrypted_content"];ok&&!null(encrypted){var value string;if scalar(encrypted,&value,"encrypted_content")!=nil||value!=""{return nil,unsupported("reasoning.encrypted_content")}}
   var parts []struct{Type string `json:"type"`;Text string `json:"text"`};if err=scalar(fields["summary"],&parts,"summary");err!=nil{return nil,err}
   if len(parts)>128{return nil,bad("summary","Too many reasoning summary blocks.")};var joined strings.Builder
   for _,part:=range parts{if part.Type!="summary_text"{return nil,unsupported("reasoning.summary.type")};joined.WriteString(part.Text);joined.WriteByte('\n')}
   // Replayed public summaries are untrusted history, not new instructions.
   if joined.Len()>0{result=append(result,Item{Type:"message",Role:"assistant",Content:[]Content{{Type:"output_text",Text:"[Prior reasoning summary]\n"+joined.String()}}})}
   continue
  }
  if kind!="message"{return nil,unsupported("input."+kind)}
  phase:="";if v,ok:=fields["phase"];ok&&!null(v){if err=scalar(v,&phase,"phase");err!=nil{return nil,err};if phase!="commentary"&&phase!="final_answer"{return nil,unsupported("phase")}}
  delete(fields,"phase")
  normalized,_:=json.Marshal(fields);array:=append(append([]byte{'['},normalized...),']')
  base:=o;base.CodexTools=false
  parsed,err:=parseItems(array,true,base);if err!=nil{return nil,err}
  for i:=range parsed{parsed[i].Phase=phase}
  result=append(result,parsed...)
 }
 if len(result)==0||len(result)>MaxItems{return nil,bad("input","No usable messages or too many input items.")};return result,nil
}

func parseCodexPreferences(fields map[string]json.RawMessage,q *Request,o Options)error{
 if raw,ok:=fields["client_metadata"];ok{
  if !o.CodexTools{return unsupported("client_metadata")}
  m,err:=Object(raw);if err!=nil||len(m)>32{return bad("client_metadata","Expected at most 32 diagnostic string fields.")}
  q.ClientMetadata=map[string]string{}
  for key,value:=range m{var text string;if len(key)>128||scalar(value,&text,"client_metadata")!=nil||len(text)>16384{return bad("client_metadata","Invalid diagnostic metadata.")};q.ClientMetadata[key]=text}
  // Kept for request inspection, never forwarded as upstream identity or authority.
 }
 if raw,ok:=fields["reasoning"];ok&&!null(raw){
  m,err:=Object(raw);if err!=nil{return err}
  if value,ok:=m["summary"];ok&&!null(value){
   if !o.CodexTools{return unsupported("reasoning.summary")}
   if err=scalar(value,&q.ReasoningSummary,"reasoning.summary");err!=nil{return err}
   switch q.ReasoningSummary{case "auto","concise","detailed":default:return unsupported("reasoning.summary")}
  }
 }
 return nil
}
func toolCallJSON(call Item)map[string]any{
 out:=map[string]any{"id":call.ID,"type":call.Type,"status":"completed","call_id":call.CallID,"name":call.Name}
 if out["type"]==""{out["type"]="function_call"}
 if call.Namespace!=""{out["namespace"]=call.Namespace}
 if call.Type=="custom_tool_call"{out["input"]=call.Input}else{out["arguments"]=call.Arguments}
 return out
}
func reasoningOutput(q *Request,result *Result,id string)map[string]any{
 if q.ReasoningSummary==""||result.ReasoningSummary==""{return nil}
 return map[string]any{"id":id,"type":"reasoning","summary":[]any{map[string]any{"type":"summary_text","text":result.ReasoningSummary}}}
}
