"""One-shot guarded integration on the feature branch. Preflight all edits."""
from pathlib import Path
files={}
def edit(p,old,new,n=1):
    text=files.get(p,Path(p).read_text())
    if text.count(old)!=n:raise RuntimeError((p,text.count(old),n,old[:120]))
    files[p]=text.replace(old,new)
def region(p,start,end,new):
    text=files.get(p,Path(p).read_text())
    if text.count(start)!=1 or text.count(end)!=1:raise RuntimeError(("region",p,start,end))
    a=text.index(start);b=text.index(end,a)
    files[p]=text[:a]+new+text[b:]

p='internal/gateway/types.go'
edit(p,'\t"context"\n','\t"context"\n\t"github.com/oai-prism/oaiprism/internal/catalog"\n')
edit(p,'type Options struct {\n','type Options struct {\n\tCatalog catalog.Options `yaml:"catalog"`\n\tCatalogEfforts map[string][]string `yaml:"-"`\n\tCodexTools bool `yaml:"codex_tools"`\n')
edit(p,'func (o Options) Validate() error {\n','func (o Options) Validate() error {\n\tif err:=o.Catalog.Validate();err!=nil{return err}\n\tif o.CodexTools && !o.PromptTools {return errors.New("codex_tools requires prompt_tools")}\n')
edit(p,'type Item struct {\n','type Item struct {\n\tNamespace string `json:"namespace,omitempty"`\n\tInput string `json:"input,omitempty"`\n\tPhase string `json:"phase,omitempty"`\n')
edit(p,'type Tool struct {\n','type Tool struct {\n\tType string `json:"type,omitempty"`\n\tNamespace string `json:"namespace,omitempty"`\n\tNamespaceDescription string `json:"namespace_description,omitempty"`\n\tFormat json.RawMessage `json:"format,omitempty"`\n')
edit(p,'type Request struct {\n','type Request struct {\n\tCodexTools bool\n\tReasoningSummary string\n\tClientMetadata map[string]string\n\tResolvedModel string\n\tAllowedAccounts []string\n\tDeclaredWindow int\n')
edit(p,'type Result struct {\n','type Result struct {\n\tReasoningSummary string\n')

p='internal/gateway/request.go'
edit(p,'allowed += " input instructions previous_response_id reasoning text max_output_tokens background truncation include context_management"','allowed += " input instructions previous_response_id reasoning text max_output_tokens background truncation include context_management client_metadata"')
edit(p,'q := &Request{Format: "text",','q := &Request{CodexTools:o.CodexTools, Format: "text",')
edit(p,'\t\t\t\t\t\terr = unsupported("reasoning.summary")','\t\t\t\t\t\tif !o.CodexTools {err = unsupported("reasoning.summary")}')
edit(p,'\t\t\tif len(values) > 0 {\n\t\t\t\terr = unsupported(k)\n\t\t\t}','\t\t\tfor _,value:=range values {if !o.CodexTools || value!="reasoning.encrypted_content" {err=unsupported(k)}}')
edit(p,'q.Tools, err = parseTools(raw, responses)','q.Tools, err = parseTools(raw, responses, o.CodexTools)')
edit(p,'func parseTools(raw []byte, responses bool) ([]Tool, error) {\n','func parseTools(raw []byte, responses bool, codex bool) ([]Tool, error) {\n\tif responses {return parseResponseTools(raw,codex)}\n')
edit(p,'func parseItems(raw []byte, responses bool, o Options) ([]Item, error) {\n','func parseItems(raw []byte, responses bool, o Options) ([]Item, error) {\n\tif responses && o.CodexTools {return parseCodexItems(raw,o)}\n')
edit(p,'\tif q.Effort != "" {\n\t\tswitch q.Effort {\n\t\tcase "none", "minimal", "low", "medium", "high", "xhigh":\n\t\tdefault:\n\t\t\treturn nil, unsupported("reasoning.effort")\n\t\t}\n\t}','\tif !validEffort(q.Model,q.Effort,o) {return nil,unsupported("reasoning.effort")}')
edit(p,'found = found || t.Name == q.ToolChoice','found = found || t.key() == q.ToolChoice')
edit(p,'\tif err := parseContextCache(m, q, o, responses); err != nil {','\tif err:=parseCodexPreferences(m,q,o);err!=nil{return nil,err}\n\tif err := parseContextCache(m, q, o, responses); err != nil {')
text=files[p];start=text.index('func ValidateHistory(items []Item) error {')
files[p]=text[:start]+'func ValidateHistory(items []Item) error { return validateCallHistory(items) }\n'

p='internal/gateway/semantics.go'
edit(p,'type Contract struct {\n','type Contract struct {\n\tcustom map[string]Tool\n')
edit(p,'c := &Contract{schemas: map[string]*jsonschema.Schema{}}','c := &Contract{schemas: map[string]*jsonschema.Schema{},custom:map[string]Tool{}}')
edit(p,'\tfor _, t := range q.Tools {\n\t\tschema, err := compileSchema(t.Parameters)','\tfor _, t := range q.Tools {\n\t\tif t.custom() {if !q.CodexTools{return nil,unsupported("tools.custom")};if err:=validateCustomFormat(t);err!=nil{return nil,err};c.custom[t.key()]=t;continue}\n\t\tschema, err := compileSchema(t.Parameters)')
edit(p,'c.schemas[t.Name] = schema','c.schemas[t.key()] = schema')
edit(p,'func PromptInstructions(q *Request) string {\n','func PromptInstructions(q *Request) string {\n\tif q.CodexTools && len(q.Tools)>0{return codexToolPrompt(q)}\n')
region(p,'\tif len(q.Tools) > 0 && len(result.Calls) == 0 {','\tif len(result.Calls) > 128 {','\tif len(q.Tools)>0 && len(result.Calls)==0 {if err:=decodeToolEnvelope(q,result);err!=nil{return err}}\n')
edit(p,'\t\tschema, ok := c.schemas[call.Name]','\t\tif t,ok:=c.custom[call.callKey()];ok {if call.Type!="custom_tool_call"{return errors.New("custom tool requires custom call type")};if q.ToolChoice!="auto"&&q.ToolChoice!="required"&&q.ToolChoice!=call.callKey(){return errors.New("upstream violated named tool selection")};if err:=validateCustomInput(t,call.Input);err!=nil{return err};continue}\n\t\tif call.Type!=""&&call.Type!="function_call"{return errors.New("function requires function_call type")}\n\t\tschema, ok := c.schemas[call.callKey()]')
edit(p,'q.ToolChoice != call.Name','q.ToolChoice != call.callKey()')
edit(p,'call.Name + call.Arguments','call.callKey() + call.Arguments + call.Input',2)

p='internal/gateway/handler.go'
edit(p,'\t"github.com/google/uuid"','\t"github.com/google/uuid"\n\t"github.com/oai-prism/oaiprism/internal/catalog"')
edit(p,'type Handler struct {\n','type Handler struct {\n\tcatalog *catalog.Registry\n')
edit(p,'\th.store, err = newStore(o)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\treturn h, nil','\th.store, err = newStore(o)\n\tif err != nil {return nil,err}\n\th.catalog,err=catalog.New(o.Catalog)\n\tif err!=nil{_ = h.store.Close();return nil,err}\n\treturn h, nil')
edit(p,'func (h *Handler) Close() error { h.summaries.clear(); return h.store.Close() }','func (h *Handler) Close() error { h.catalog.Close();h.summaries.clear(); return h.store.Close() }')
edit(p,'\tpath := r.URL.Path\n','\tif h.serveCatalog(w,r){return}\n\tpath := r.URL.Path\n')
edit(p,'Parse(body, responses, h.options)','h.parseRequest(body, responses)')
edit(p,'\t\tresult.Calls[i].Type = "function_call"','\t\tif result.Calls[i].Type=="" {result.Calls[i].Type = "function_call"}')
p='internal/gateway/context_endpoint.go'
edit(p,'Parse(body, true, h.options)','h.parseRequest(body, true)')

p='internal/gateway/context_cache.go'
edit(p,'if item.Type == "function_call" {','if isToolCall(item.Type) {')
edit(p,'if item.Type == "function_call_output" {','if isToolOutput(item.Type) {')
edit(p,'"gateway-transcript-v2"','"gateway-transcript-v3"')
edit(p,'\troute := h.options.Models[q.Model]','\troute := h.options.Models[q.Model]\n\tif q.ResolvedModel!=""{route=q.ResolvedModel}')
edit(p,'\tbudget := c.window(q.Model) - reserve - margin','\twindow:=c.window(q.Model)\n\tif q.DeclaredWindow>0 && (window==0 || q.DeclaredWindow<window){window=q.DeclaredWindow}\n\tbudget := window - reserve - margin')
p='internal/gateway/context_compaction.go'
edit(p,'case "function_call":','case "function_call", "custom_tool_call":')
edit(p,'case "function_call_output":','case "function_call_output", "custom_tool_call_output":')
edit(p,'return &Request{Model: q.Model,','return &Request{ResolvedModel:q.ResolvedModel,AllowedAccounts:append([]string(nil),q.AllowedAccounts...),DeclaredWindow:q.DeclaredWindow,Model: q.Model,')

p='internal/facade/gateway_adapter.go'
edit(p,'\tinput := gatewayInput(q)','\tif q.ResolvedModel!="" {model=q.ResolvedModel}\n\tinput := gatewayInput(q)')
edit(p,'run := &RunRequest{Input: input,','run := &RunRequest{AllowedAccounts:append([]string(nil),q.AllowedAccounts...),Input: input,')
edit(p,'out := &gateway.Result{Text: result.Text}','out := &gateway.Result{Text: result.Text}\n\tif q.ReasoningSummary!="" && len(result.Reasoning)<=1<<20 {out.ReasoningSummary=result.Reasoning}')
p='internal/facade/runner.go'
edit(p,'type RunRequest struct {\n','type RunRequest struct {\n\tAllowedAccounts []string\n')
edit(p,'func (r *Runner) acquire(ctx context.Context, req *RunRequest) (*account.Lease, error) {\n','func (r *Runner) acquire(ctx context.Context, req *RunRequest) (*account.Lease, error) {\n\tif req.AllowedAccounts!=nil{return r.acquireCatalog(ctx,req)}\n')

p='internal/gateway/stream.go'
edit(p,'return map[string]any{"id": call.ID, "type": "function_call", "status": "completed", "call_id": call.CallID, "name": call.Name, "arguments": call.Arguments}','return toolCallJSON(call)')
edit(p,'\t\t\tadded["arguments"] = ""','\t\t\tfield,eventPrefix,value:="arguments","response.function_call_arguments",call.Arguments\n\t\t\tif call.Type=="custom_tool_call"{field,eventPrefix,value="input","response.custom_tool_call_input",call.Input}\n\t\t\tadded[field] = ""')
edit(p,'s.event("response.function_call_arguments.delta", map[string]any{"item_id": call.ID, "output_index": outIndex, "delta": call.Arguments})','s.event(eventPrefix+".delta", map[string]any{"item_id": call.ID, "output_index": outIndex, "delta": value})')
edit(p,'s.event("response.function_call_arguments.done", map[string]any{"item_id": call.ID, "output_index": outIndex, "name": call.Name, "arguments": call.Arguments})','s.event(eventPrefix+".done", map[string]any{"item_id": call.ID, "output_index": outIndex, "name": call.Name, field: value})')
region(p,'\ttools := []any{}\n\tfor _, t := range q.Tools {','\tvar choice any = q.ToolChoice','\ttools := toolDefinitions(q)\n')
edit(p,'\tfor index, call := range result.Calls {','\tif item:=reasoningOutput(s.q,result,"rs_"+uuid.NewString());item!=nil && s.responses {idx:=len(output);output=append(output,item);if err:=s.event("response.output_item.added",map[string]any{"output_index":idx,"item":item});err!=nil{return nil,err};if err:=s.event("response.output_item.done",map[string]any{"output_index":idx,"item":item});err!=nil{return nil,err}}\n\tfor index, call := range result.Calls {')
edit(p,'\tfor _, call := range result.Calls {\n\t\toutput = append(output, callJSON(call))','\tif result.ReasoningSummary!=""{output=append(output,map[string]any{"id":"rs_"+uuid.NewString(),"type":"reasoning","summary":[]any{map[string]any{"type":"summary_text","text":result.ReasoningSummary}}})}\n\tfor _, call := range result.Calls {\n\t\toutput = append(output, callJSON(call))')
for p,content in files.items():Path(p).write_text(content)
print("Integrated",len(files),"files")
