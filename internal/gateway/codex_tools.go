package gateway

import (
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "errors"
 "regexp"
 "strings"
)

// Codex support is explicit. No substring search in the user's prompt can enable
// a tool bridge, execute a command, or grant filesystem/approval privileges.
func toolKey(namespace,name string)string{if namespace!=""{return namespace+"."+name};return name}
func (t Tool) key()string{return toolKey(t.Namespace,t.Name)}
func (t Tool) custom()bool{return t.Type=="custom"}
func (i Item) callKey()string{return toolKey(i.Namespace,i.Name)}
func isToolCall(kind string)bool{return kind=="function_call"||kind=="custom_tool_call"}
func isToolOutput(kind string)bool{return kind=="function_call_output"||kind=="custom_tool_call_output"}

func parseResponseTools(raw []byte,codex bool)([]Tool,error){
 var list []json.RawMessage;if err:=scalar(raw,&list,"tools");err!=nil{return nil,err};if len(list)>128{return nil,bad("tools","At most 128 tool definitions are allowed.")}
 result:=[]Tool{};seen:=map[string]bool{}
 var parse func(json.RawMessage,string)error
 parse=func(raw json.RawMessage,ns string)error{
  if len(result)>=128{return bad("tools","At most 128 callable tools are allowed.")}
  m,err:=Object(raw);if err!=nil{return err};var typ,name string
  if err=scalar(m["type"],&typ,"tools.type");err!=nil{return err}
  if typ!="function"&&(!codex||typ!="custom"&&typ!="namespace"){return unsupported("tools.type")}
  if err=scalar(m["name"],&name,"tools.name");err!=nil{return err};if !functionName.MatchString(name){return bad("tools.name","Invalid tool name.")}
  if typ=="namespace"{
   if ns!=""{return unsupported("tools.namespace.nesting")};if err=keys(m,"type name description tools");err!=nil{return err}
   if v,ok:=m["description"];ok{var description string;if scalar(v,&description,"description")!=nil||len(description)>16384{return bad("tools.description","Invalid namespace description.")}}
   var tools []json.RawMessage;if err=scalar(m["tools"],&tools,"tools");err!=nil{return err};if len(tools)==0||len(tools)>128{return bad("tools","Empty or oversized tool namespace.")}
   // Namespace descriptions are retained on each member, not executable code.
   for _,child:=range tools{before:=len(result);if err=parse(child,name);err!=nil{return err};if len(result)>before{_ = json.Unmarshal(m["description"],&result[len(result)-1].NamespaceDescription)}}
   return nil
  }
  allowed:="type name description parameters strict";if typ=="custom"{allowed="type name description format"}
  if err=keys(m,allowed);err!=nil{return err}
  t:=Tool{Type:typ,Namespace:ns,Name:name};if seen[t.key()]{return bad("tools.name","Duplicate qualified tool name.")};seen[t.key()]=true
  if v,ok:=m["description"];ok{if err=scalar(v,&t.Description,"tools.description");err!=nil{return err};if len(t.Description)>64<<10{return bad("tools.description","Tool description too large.")}}
  if typ=="custom"{
   t.Format=m["format"];if len(t.Format)==0{t.Format=json.RawMessage(`{"type":"text"}`)}
   if err=validateCustomFormat(t);err!=nil{return err}
  }else{
   if v,ok:=m["strict"];ok{if err=scalar(v,&t.Strict,"tools.strict");err!=nil{return err}}
   t.Parameters=m["parameters"];if len(t.Parameters)==0{t.Parameters=json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
  }
  result=append(result,t);return nil
 }
 for _,raw:=range list{if err:=parse(raw,"");err!=nil{return nil,err}}
 return result,nil
}

// General Lark execution is deliberately not a server feature. This fingerprint
// is the public apply_patch grammar in Codex rust-v0.160.0 (not copied source).
const codexPatchGrammarSHA="50b06e74592ca36baf0a0804a36d76aef1847b251c7b5b5bfd9bc81ec7794023"
func validateCustomFormat(t Tool)error{
 if len(t.Format)>64<<10{return bad("tools.format","Custom grammar too large.")}
 m,err:=Object(t.Format);if err!=nil{return err};var typ string;if err=scalar(m["type"],&typ,"tools.format");err!=nil{return err}
 if typ=="text"{return keys(m,"type")}
 if typ!="grammar"{return unsupported("tools.format.type")}
 if err=keys(m,"type syntax definition");err!=nil{return err};var syntax,definition string
 if scalar(m["syntax"],&syntax,"syntax")!=nil||scalar(m["definition"],&definition,"definition")!=nil{return bad("tools.format","Invalid grammar definition.")}
 switch syntax{
 case "regex":_,err=regexp.Compile("\\A(?:"+definition+")\\z");if err!=nil{return unsupported("tools.format.regex")}
 case "lark":digest:=sha256.Sum256([]byte(strings.TrimSpace(definition)));if t.Name!="apply_patch"||hex.EncodeToString(digest[:])!=codexPatchGrammarSHA{return unsupported("tools.format.lark")}
 default:return unsupported("tools.format.syntax")
 }
 return nil
}
func validateCustomInput(t Tool,input string)error{
 if len(input)>MaxOutput||strings.ContainsRune(input,0){return errors.New("invalid custom tool input")}
 var format struct{Type,Syntax,Definition string};if json.Unmarshal(t.Format,&format)!=nil{return errors.New("invalid custom format")}
 if format.Type=="text"{return nil}
 if format.Syntax=="regex"{r,err:=regexp.Compile("\\A(?:"+format.Definition+")\\z");if err!=nil||!r.MatchString(input){return errors.New("custom input violates declared grammar")};return nil}
 return validatePatchSyntax(input)
}
// validatePatchSyntax matches the admitted patch grammar, not filesystem paths.
// Codex owns path authorization, sandbox enforcement, patch application and tests.
func validatePatchSyntax(input string)error{
 lines:=strings.Split(strings.TrimSuffix(input,"\n"),"\n")
 if len(lines)<3||lines[0]!="*** Begin Patch"||lines[len(lines)-1]!="*** End Patch"{return errors.New("invalid patch framing")}
 at,hunks:=1,0;end:=len(lines)-1
 for at<end{
  header:=lines[at];at++;hunks++
  switch{
  case strings.HasPrefix(header,"*** Add File: ")&&len(header)>14:
   n:=0;for at<end&&strings.HasPrefix(lines[at],"+"){at++;n++};if n==0{return errors.New("empty add-file patch")}
  case strings.HasPrefix(header,"*** Delete File: ")&&len(header)>17:
  case strings.HasPrefix(header,"*** Update File: ")&&len(header)>17:
   if at<end&&strings.HasPrefix(lines[at],"*** Move to: "){if len(lines[at])<=13{return errors.New("empty move target")};at++}
   for at<end{line:=lines[at];if line=="*** End of File"{at++;break};if line=="@@"||strings.HasPrefix(line,"@@ ")||len(line)>0&&strings.ContainsRune("+- ",rune(line[0])){at++;continue};break}
  default:return errors.New("invalid patch hunk")
  }
 }
 if hunks==0{return errors.New("patch has no hunks")};return nil
}
func toolDefinitions(q *Request)[]any{
 out:=[]any{};namespaces:=map[string]map[string]any{}
 for _,t:=range q.Tools{
  var definition map[string]any
  if t.custom(){var format any;_ = json.Unmarshal(t.Format,&format);definition=map[string]any{"type":"custom","name":t.Name,"description":t.Description,"format":format}}else{var parameters any;_ = json.Unmarshal(t.Parameters,&parameters);definition=map[string]any{"type":"function","name":t.Name,"description":t.Description,"parameters":parameters,"strict":t.Strict}}
  if t.Namespace==""{out=append(out,definition);continue}
  parent,ok:=namespaces[t.Namespace];if !ok{parent=map[string]any{"type":"namespace","name":t.Namespace,"description":t.NamespaceDescription,"tools":[]any{}};namespaces[t.Namespace]=parent;out=append(out,parent)}
  parent["tools"]=append(parent["tools"].([]any),definition)
 }
 return out
}
func codexToolPrompt(q *Request)string{
 definitions,_:=json.Marshal(q.Tools)
 return "\n\n[Client-owned tool contract]\nTools below run ONLY on the caller's client. Do not use your private workspace/terminal to substitute for them. Preserve all prior system/developer constraints and approvals. Never claim a local edit or test succeeded without a client result. Return ONE JSON object, no fences: {\"text\":\"answer or empty\",\"tool_calls\":[]}. For a function call use {\"name\":\"exact name\",\"namespace\":\"declared namespace or empty\",\"arguments\":{...}}. For a custom tool use {\"name\":\"exact name\",\"namespace\":\"declared namespace or empty\",\"input\":\"raw input satisfying its declared format\"}. Never invent tools, namespaces, execution results or approvals. A final answer/review uses an empty tool_calls array. tool_choice="+q.ToolChoice+". parallel_tool_calls="+boolText(q.Parallel)+". Definitions: "+string(definitions)
}
func boolText(v bool)string{if v{return "true"};return "false"}
func decodeToolEnvelope(q *Request,result *Result)error{
 fields,err:=Object([]byte(strings.TrimSpace(result.Text)));if err!=nil||keys(fields,"text tool_calls")!=nil{return errors.New("upstream did not return the declared tool envelope")}
 var text string;var calls []json.RawMessage
 if scalar(fields["text"],&text,"")!=nil||scalar(fields["tool_calls"],&calls,"")!=nil||len(calls)>128{return errors.New("invalid tool envelope")}
 byName:=map[string]Tool{};for _,t:=range q.Tools{byName[t.key()]=t}
 parsed:=[]Item{}
 for _,raw:=range calls{
  m,e:=Object(raw);if e!=nil||keys(m,"name namespace arguments input")!=nil{return errors.New("invalid tool call fields")}
  var name,ns string;if scalar(m["name"],&name,"")!=nil{return errors.New("missing tool name")};if v,ok:=m["namespace"];ok{if scalar(v,&ns,"")!=nil{return errors.New("invalid tool namespace")}}
  tool,ok:=byName[toolKey(ns,name)];if !ok{return errors.New("undeclared tool or namespace")}
  item:=Item{Type:"function_call",Name:name,Namespace:ns}
  if tool.custom(){item.Type="custom_tool_call";if _,ok:=m["arguments"];ok{return errors.New("custom tool must use input, not arguments")};if scalar(m["input"],&item.Input,"")!=nil{return errors.New("custom tool input must be text")}}else{if _,ok:=m["input"];ok{return errors.New("function must use arguments, not input")};if len(m["arguments"])==0||null(m["arguments"]){return errors.New("missing function arguments")};item.Arguments=string(m["arguments"])}
  parsed=append(parsed,item)
 }
 result.Text=text;result.Calls=parsed;return nil
}
func validateCallHistory(items []Item)error{
 pending:=map[string]string{};seen:=map[string]bool{}
 for _,item:=range items{
  if isToolCall(item.Type){if item.CallID==""||len(item.CallID)>128||seen[item.CallID]{return bad("input","Duplicate or invalid tool call ID.")};seen[item.CallID]=true;pending[item.CallID]=item.Type}
  if isToolOutput(item.Type){kind,ok:=pending[item.CallID];if !ok||item.Type!=kind+"_output"{return bad("input","Tool output must match a preceding unresolved call and its type.")};delete(pending,item.CallID)}
 }
 if len(pending)>0{return bad("input","Provide outputs for all preceding tool calls.")};return nil
}
