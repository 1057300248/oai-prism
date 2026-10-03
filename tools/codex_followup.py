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
for p,text in files.items():Path(p).write_text(text)
print('Follow-up integration:',len(files),'files')
