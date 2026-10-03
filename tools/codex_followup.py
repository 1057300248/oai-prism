"""Apply after codex_integrate.py and gofmt; all substitutions preflight."""
from pathlib import Path
files={}
def edit(p,old,new,n=1):
    text=files.get(p,Path(p).read_text())
    assert text.count(old)==n,(p,text.count(old),old[:100])
    files[p]=text.replace(old,new)
def region(p,start,end,new):
    text=files.get(p,Path(p).read_text())
    assert text.count(start)==1 and text.count(end)==1,(p,start,end)
    a=text.index(start);b=text.index(end,a);files[p]=text[:a]+new+text[b:]
p='internal/gateway/model_catalog.go'
edit(p,'\tdeclared := map[string]bool{}','\tdeclared := map[string]bool{}\n\tfor _,id:=range h.options.Catalog.Publish {declared[id]=true;options.Models[id]=id}')
edit(p,'\toptions.CatalogEfforts = map[string][]string{}','\toptions.CatalogEfforts = map[string][]string{}\n\toptions.Context.ModelWindows=map[string]int{}\n\tfor id,w:=range h.options.Context.ModelWindows{options.Context.ModelWindows[id]=w}')
edit(p,'\t\t\tif r.ID == id {','\t\t\tif r.ID == id {\n\t\t\t\tif !r.Stale&&r.ContextWindow>0 {w:=options.Context.window(id);if w==0||r.ContextWindow<w{options.Context.ModelWindows[id]=r.ContextWindow}}')
edit(p,'\t\t"slug": m.ID,','\t\t"model_messages":map[string]any{"instructions_template":codexInstructions},\n\t\t"slug": m.ID,')
# Public model lists hide stale entries, while capability diagnostics retain them.
edit(p,'\t\tentry, ok := byID[r.ID]','\t\tentry, ok := byID[r.ID]') if False else None
edit(p,'\tfor _, r := range h.catalog.Records() {\n\t\tif !h.catalog.Published(r.ID) {','\tfor _,id:=range h.options.Catalog.Publish {byID[id]=map[string]any{"id":id,"object":"model","created":0,"owned_by":"oaiprism","x_oaiprism_evidence":"unavailable","x_oaiprism_stale":true,"reasoning_efforts":[]string{}}}\n\tfor _, r := range h.catalog.Records() {\n\t\tif !h.catalog.Published(r.ID) {')
edit(p,'if !ok || entry["x_oaiprism_evidence"] == "configured" {','if !ok || entry["x_oaiprism_evidence"] == "configured" || entry["x_oaiprism_evidence"] == "unavailable" {')
edit(p,'\t\tfor _, effort := range r.ReasoningEfforts {','\t\tfor _, effort := range r.ReasoningEfforts {\n\t\t\tif r.Stale {continue}')
edit(p,'\tentries := h.modelEntries()','\tentries := h.modelEntries()\n\tif path!="/v1/model-capabilities" {fresh:=[]map[string]any{};for _,entry:=range entries{if entry["x_oaiprism_stale"]!=true{fresh=append(fresh,entry)}};entries=fresh}')
edit(p,'\t\t\tresult = append(result, CodexModel(model, h.options.CodexTools && h.options.PromptTools))','\t\t\tif efforts,ok:=entry["reasoning_efforts"].([]string);ok&&len(efforts)>0{model.ReasoningEfforts=efforts}\n\t\t\tresult = append(result, CodexModel(model, h.options.CodexTools && h.options.PromptTools))')
p='internal/gateway/request.go'
region(p,'\t\tcase "tool_choice":','\t\tcase "text", "response_format":','\t\tcase "tool_choice":\n\t\t\tq.ToolChoice,err=parseToolChoice(raw,responses,o.CodexTools)\n')
edit(p,'if q.Format != "text" && len(q.Tools) > 0 {','if q.Format != "text" && len(q.Tools) > 0 && !q.CodexTools {')
p='internal/gateway/semantics.go'
edit(p,'\tif q.Format != "text" {','\tif q.Format != "text" && len(result.Calls)==0 {')
p='internal/gateway/codex_tools.go'
edit(p,'+ string(definitions)\n','+ string(definitions) + finalFormatInstruction(q)\n')
p='internal/gateway/stream.go'
region(p,'\tvar choice any = q.ToolChoice','\tformat := map[string]any', '\tchoice := selectedToolJSON(q)\n')
p='internal/gateway/context_cache.go'
region(p,'\twindow := c.window(q.Model)','\tbudget := window - reserve - margin','\twindow := c.effectiveWindow(q)\n')
edit(p,'func (c ContextPolicy) summaryLimit() int {','func (c ContextPolicy) effectiveWindow(q *Request) int {w:=c.window(q.Model);if q.DeclaredWindow>0&&(w==0||q.DeclaredWindow<w){w=q.DeclaredWindow};return w}\n\nfunc (c ContextPolicy) summaryLimit() int {')
p='internal/gateway/context_compaction.go'
edit(p,'\tpolicy := h.options.Context','\tpolicy := h.options.Context\n\troute:=h.options.Models[q.Model];if q.ResolvedModel!=""{route=q.ResolvedModel}')
edit(p,'q.Model, h.options.Models[q.Model], q.Effort','q.Model, route, q.Effort')
edit(p,'policy.window(q.Model)','policy.effectiveWindow(q)')
edit(p,'summaryBudget := c.window(q.Model)','summaryBudget := c.effectiveWindow(q)')
p='internal/facade/gateway_adapter.go'
edit(p,'if mapping, ok := g.cfg.Facade.Models[model]; ok {','if mapping, ok := g.cfg.Facade.Models[model]; ok && q.ResolvedModel=="" {')
p='internal/gateway/handler.go'
edit(p,'"object": "gateway.capabilities",','"object": "gateway.capabilities", "codex_tools":h.options.CodexTools,"codex_client_target":"0.160.0","model_discovery":map[string]any{"enabled":len(h.options.Catalog.Sources)>0,"evidence":"declared_not_live_verified","auto_publish":false},')
for p,text in files.items():Path(p).write_text(text)
print('Follow-up integration:',len(files),'files')
