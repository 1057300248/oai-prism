"""Follow-up guarded edits; run after primary integration and gofmt."""
from pathlib import Path
files={}
def edit(p,old,new,n=1):
    text=files.get(p,Path(p).read_text())
    assert text.count(old)==n,(p,text.count(old),old[:100])
    files[p]=text.replace(old,new)

edit('internal/prism/client.go','\t\t\t\tpayload := env.Response.Payload\n','\t\t\t\tpayload := env.Response.Payload\n\t\t\t\tif payload.Usage != nil { out.Usage = payload.Usage }\n')
edit('internal/gateway/handler.go','\tresult, err := h.engine.Run(ctx, q, accepted, emit)','\tfor i := range q.Items { if q.Items[i].ID == "" { q.Items[i].ID = "item_" + uuid.NewString() } }\n\tresult, err := h.engine.Run(ctx, q, accepted, emit)')
edit('internal/gateway/context_cache.go','\tfor _, model := range h.options.Cache.NativeModels {','\tq.NativeCacheForward = false\n\tfor _, model := range h.options.Cache.NativeModels {')

p='internal/gateway/context_compaction.go'
edit(p,'type summaryCache struct {\n','type summaryCache struct {\n\tclosed bool\n')
edit(p,'func (c *summaryCache) getLocked(key string) (string, bool) {\n','func (c *summaryCache) getLocked(key string) (string, bool) {\n\tif c.closed { return "",false }\n')
edit(p,'func (c *summaryCache) build(ctx context.Context, key string, ttl time.Duration, fn func() (string, error)) (string, bool, error) {\n\tc.mu.Lock()','func (c *summaryCache) build(ctx context.Context, key string, ttl time.Duration, fn func() (string, error)) (string, bool, error) {\n\tif err:=ctx.Err();err!=nil{return "",false,err}\n\tc.mu.Lock()\n\tif c.closed {c.mu.Unlock();return "",false,errors.New("summary cache closed")}')
edit(p,'\ttext, err := fn()','\ttext, err := safeSummaryBuild(fn)')
edit(p,'\tif err == nil && len(text) <= 256<<10 {','\tif c.closed && err==nil {err=errors.New("summary cache closed")}\n\tif err == nil && len(text) <= 256<<10 {')
edit(p,'clear(c.entries)','c.closed = true; clear(c.entries)')
edit(p,'func (c *summaryCache) clear() {','''func safeSummaryBuild(fn func()(string,error))(text string,err error){
    defer func(){if recover()!=nil{err=errors.New("summarizer failed unexpectedly")}}()
    return fn()
}

func (c *summaryCache) clear() {''')

p='tools/gateway-fixture/main.go'
edit(p,'\ttext := "Hello world"','''    if q.InternalSummary {
        return &gateway.Result{Text:"Goals: continue task. Facts: preserve constraints and src/main.go. Decisions: unchanged. Open work: next step.",Usage:&gateway.Usage{Input:40,Output:8,Source:"upstream"}},nil
    }
\ttext := "Hello world"''')
edit(p,'gateway.Options{Enabled: true,','''gateway.Options{Context:gateway.ContextPolicy{Enabled:true,WindowTokens:8192,OutputReserve:512,SafetyMargin:128,SummaryTokens:128,KeepLastTurns:1,SummaryCache:true},Cache:gateway.PromptCachePolicy{Affinity:true,NativeModels:[]string{"test-model"}},Enabled: true,''')

for p,s in files.items():Path(p).write_text(s)
print('Follow-up corrections:',len(files),'files')
