"""Guarded one-shot integration on the dedicated PR branch only."""
from pathlib import Path

files={}
def get(p):
    if p not in files: files[p]=Path(p).read_text()
    return files[p]
def edit(p,old,new,n=1):
    text=get(p)
    assert text.count(old)==n,(p,text.count(old),old[:120])
    files[p]=text.replace(old,new)

t='internal/gateway/types.go'
edit(t,'type Options struct {\n','type Options struct {\n\tContext ContextPolicy `yaml:"context"`\n\tCache PromptCachePolicy `yaml:"prompt_cache"`\n')
edit(t,'func (o Options) Validate() error {\n','func (o Options) Validate() error {\n\tif err:=o.validateContextCache();err!=nil {return err}\n')
edit(t,'type Request struct {\n','''type Request struct {
    PromptCacheKey string
    PromptCacheRetention string
    PromptCacheOptions map[string]json.RawMessage
    ScopedCacheKey string
    CacheAffinity bool
    NativeCacheForward bool
    CompactThreshold int
    InternalSummary bool
    ContextReport *ContextReport
    ContextUsage *Usage
''')
edit(t,'type Usage struct {\n','type Usage struct {\n\tCacheWrite *int\n\tContext *ContextReport\n')
edit(t,'type APIError struct {\n','type APIError struct {\n\tContext *ContextReport\n')
edit(t,'return map[string]any{"error": map[string]any{"message": api.Message, "type": typ, "code": api.Code, "param": param}}','body:=map[string]any{"error": map[string]any{"message": api.Message, "type": typ, "code": api.Code, "param": param}}\n\tif api.Context!=nil{body["x_oaiprism_context"]=api.Context}\n\treturn body')

p='internal/gateway/request.go'
edit(p,'allowed := "model stream store metadata user safety_identifier prompt_cache_key tools tool_choice parallel_tool_calls"','allowed := "model stream store metadata user safety_identifier prompt_cache_key prompt_cache_retention prompt_cache_options tools tool_choice parallel_tool_calls"')
edit(p,'allowed += " input instructions previous_response_id reasoning text max_output_tokens background truncation include"','allowed += " input instructions previous_response_id reasoning text max_output_tokens background truncation include context_management"')
# Dedicated parser must retain controls instead of throwing them away.
edit(p,'case "user", "safety_identifier", "prompt_cache_key":','case "user", "safety_identifier":')
edit(p,'\treturn q, nil\n','\tif err:=parseContextCache(m,q,o,responses);err!=nil{return nil,err}\n\treturn q, nil\n')

p='internal/gateway/handler.go'
edit(p,'type Handler struct {\n','type Handler struct {\n\tsummaries *summaryCache\n')
edit(p,'h := &Handler{options: o, engine: engine}','h := &Handler{options: o, engine: engine, summaries:newSummaryCache()}')
edit(p,'func (h *Handler) Close() error { return h.store.Close() }','func (h *Handler) Close() error { h.summaries.clear();return h.store.Close() }')
edit(p,'case path == "/v1/chat/completions" || path == "/chat/completions":','''case path == "/v1/responses/compact" || path == "/responses/compact":
        if r.Method!="POST"{h.method(w,"POST");return}
        h.contextEndpoint(w,r,owner,false);return
    case path == "/v1/responses/input_tokens":
        if r.Method!="POST"{h.method(w,"POST");return}
        h.contextEndpoint(w,r,owner,true);return
    case path == "/v1/chat/completions" || path == "/chat/completions":''')
edit(p,'"object": "gateway.capabilities",','"object": "gateway.capabilities", "context":h.contextCapabilities(), "prompt_cache":h.cacheCapabilities(),')
# Put the deadline before body parsing, context-store lookup and summarization.
block='''\ttimeout := h.options.Timeout
\tif timeout == 0 {
\t\ttimeout = 10 * time.Minute
\t}
\tctx, cancel := context.WithTimeout(r.Context(), timeout)
\tdefer cancel()
'''
edit(p,block,'')
anchor='func (h *Handler) generate(w http.ResponseWriter, r *http.Request, owner string, responses bool) {\n'
edit(p,anchor,anchor+block)
edit(p,'h.store.Get(r.Context(), q.PreviousID, owner)','h.store.Get(ctx, q.PreviousID, owner)')
edit(p,'\tresult, err := h.engine.Run(ctx, q, accepted, emit)','''    h.bindPromptCache(q,owner)
    cacheMode:="disabled"
    if q.CacheAffinity {cacheMode="scoped-affinity"}
    if q.NativeCacheForward {cacheMode="native-parameters-forwarded"}
    w.Header().Set("X-Oaiprism-Prompt-Cache",cacheMode)
    if err:=h.prepareContext(ctx,q,owner,false,accepted);err!=nil {
        setContextHeaders(w,q)
        h.fail(w,r,s,withContextReport(q,err));return
    }
    setContextHeaders(w,q)
\tresult, err := h.engine.Run(ctx, q, accepted, emit)''')
# Failures after compaction still report the extra calls/usage, never hide them.
edit(p,'\t\th.fail(w, r, s, err)','\t\th.fail(w, r, s, withContextReport(q,err))',3)

p='internal/gateway/semantics.go'
edit(p,'func NormalizeUsage(q *Request, result *Result, policy string) (*Usage, error) {','func normalizeGenerationUsage(q *Request, result *Result, policy string) (*Usage, error) {')
s=get(p);start=s.index('\t\tvar text strings.Builder',s.index('func normalizeGenerationUsage'));end=s.index('\t\tif err != nil {',s.index('\t\tin, err := CountTokens(text.String())',start))
files[p]=s[:start]+'\t\tin, err := RenderedTokens(q)\n'+s[end:]
edit(p,'\treturn u, nil\n','''    if u.CacheWrite!=nil && (*u.CacheWrite<0||*u.CacheWrite>u.Input) {return nil,errors.New("invalid cache write token count")}
    if u.Cached!=nil && u.CacheWrite!=nil && *u.Cached+*u.CacheWrite>u.Input {return nil,errors.New("overlapping cache read/write usage")}
\treturn u, nil
''')
edit(p,'\tif u.Cached != nil {\n\t\tm[idetail] = map[string]any{"cached_tokens": *u.Cached}\n\t}', '''    detail:=map[string]any{}
    if u.Cached!=nil {detail["cached_tokens"]=*u.Cached}
    if u.CacheWrite!=nil {detail["cache_write_tokens"]=*u.CacheWrite}
    if len(detail)>0 {m[idetail]=detail}
    if u.Context!=nil {m["x_oaiprism_context"]=u.Context}''')

p='internal/gateway/stream.go'
edit(p,'s.w.Header().Add("Trailer", "X-Oaiprism-Usage-Source")','''s.w.Header().Add("Trailer", "X-Oaiprism-Usage-Source")
    for _,name:=range []string{"X-Oaiprism-Context-Before","X-Oaiprism-Context-After","X-Oaiprism-Summary-Calls","X-Oaiprism-Summary-Cache-Hits"}{s.w.Header().Add("Trailer",name)}''')
edit(p,'"object": "response",','"object": "response", "x_oaiprism_context":q.ContextReport,')
edit(p,'"object": "chat.completion",','"object": "chat.completion", "x_oaiprism_context":q.ContextReport,')
# Stream failure carries local-summary usage even though no success is emitted.
edit(p,'response["error"] = map[string]any{"code": api.Code, "message": api.Message}','response["error"] = map[string]any{"code": api.Code, "message": api.Message}\n\t\tif api.Context!=nil{response["x_oaiprism_context"]=api.Context}')

p='internal/facade/gateway_adapter.go'
edit(p,'run := &RunRequest{Input: input, Model: model, Effort: effort, API: "gateway", Isolated: true}','''run := &RunRequest{Input: input, Model: model, Effort: effort, API: "gateway", Isolated: true,Extra:q.NativeCacheFields()}
    if q.CacheAffinity {run.StickyKey=q.ScopedCacheKey}''')
edit(p,'Cached: u.CachedTokens,','Cached: u.CachedTokens, CacheWrite:u.CacheWriteTokens,')
s=get(p);start=s.index('func gatewayInput(');end=s.index('// preprocessGatewayImages',start)
files[p]=s[:start]+'''func gatewayInput(q *gateway.Request) []prism.InputItem {
    rendered:=gateway.RenderInput(q)
    items:=make([]prism.InputItem,0,len(rendered))
    for _,message:=range rendered {
        item:=prism.InputItem{Type:message.Type,Role:message.Role}
        for _,part:=range message.Content{item.Content=append(item.Content,prism.InputContent{Type:part.Type,Text:part.Text,ImageURL:part.ImageURL,Detail:part.Detail})}
        items=append(items,item)
    }
    return items
}

'''+s[end:]
edit(p,'\t"encoding/json"\n','')

p='internal/prism/types.go'
edit(p,'type Usage struct {\n','type Usage struct {\n\tCacheWriteTokens *int `json:"-"`\n')
# The full usage parser is rewritten separately with alias conflict checks.

for p,s in files.items(): Path(p).write_text(s)
print('Integrated',len(files),'files')
