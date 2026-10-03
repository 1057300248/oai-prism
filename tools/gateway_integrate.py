"""One-shot guarded integration. All replacements preflight before any writes.
Run only on the dedicated recovery branch; the workflow commits real Go source.
"""
from pathlib import Path
import re

files = {}
def load(path):
    if path not in files: files[path] = Path(path).read_text(encoding='utf-8')
    return files[path]
def replace(path, old, new, count=1):
    text=load(path)
    found=text.count(old)
    if found != count: raise RuntimeError(f'{path}: expected {count} exact matches, got {found}: {old[:90]!r}')
    files[path]=text.replace(old,new)
def regex(path, pattern, replacement, count=1):
    text=load(path)
    text,n=re.subn(pattern,replacement,text,flags=re.MULTILINE)
    if n!=count: raise RuntimeError(f'{path}: regex expected {count}, got {n}: {pattern[:80]}')
    files[path]=text

p='internal/config/config.go'
replace(p,'\t"gopkg.in/yaml.v3"','\t"gopkg.in/yaml.v3"\n\t"github.com/oai-prism/oaiprism/internal/gateway"')
replace(p,'type FacadeConfig struct {\n','type FacadeConfig struct {\n\tGateway gateway.Options `yaml:"gateway"`\n')

p='internal/server/server.go'
replace(p,'\t"github.com/oai-prism/oaiprism/internal/facade"','\t"github.com/oai-prism/oaiprism/internal/facade"\n\t"github.com/oai-prism/oaiprism/internal/gateway"')
replace(p,'type Server struct {\n','type Server struct {\n\tgateway *gateway.Handler\n\trunner *facade.Runner\n')
replace(p,'func New(cfg *config.Config, log *slog.Logger) (*Server, error) {\n','''func New(cfg *config.Config, log *slog.Logger) (*Server, error) {
    gatewayOptions := cfg.Facade.Gateway
    gatewayOptions.APIKeys = cfg.Facade.APIKeys
    gatewayOptions.Models = make(map[string]string, len(cfg.Facade.Models)+1)
    for name, mapping := range cfg.Facade.Models { gatewayOptions.Models[name] = mapping.Model }
    if cfg.Facade.DefaultModel != "" { gatewayOptions.Models[cfg.Facade.DefaultModel] = cfg.Facade.DefaultModel }
    if gatewayOptions.Enabled {
        if !cfg.Facade.Enabled || cfg.Capture.Enabled { return nil, fmt.Errorf("gateway requires facade enabled and capture disabled") }
        if err := gatewayOptions.Validate(); err != nil { return nil, err }
    }
''')
replace(p,'\t// 5) 路由。\n','''    s.runner = runner
    if gatewayOptions.Enabled {
        public, err := gateway.New(gatewayOptions, facade.NewGatewayEngine(cfg, runner))
        if err != nil { _ = s.Close(); return nil, err }
        s.gateway = public
    }
\t// 5) 路由。
''')
replace(p,'\t\tfacadeHandler.Register(mux)','\t\tif s.gateway != nil { mux.Handle("/", s.gateway) } else { facadeHandler.Register(mux) }')
replace(p,'\trawHandler.Register(mux)','\tif s.gateway == nil { rawHandler.Register(mux) }')
replace(p,'\tmux.HandleFunc("GET /admin/accounts",','\tif cfg.Facade.Gateway.Enabled { return }\n\n\tmux.HandleFunc("GET /admin/accounts",')
replace(p,'\t\tif s.pool.Size() == 0 {','\t\tif s.pool.Size() == 0 || healthy == 0 {')
replace(p,'func (s *Server) requestAuditMiddleware(next http.Handler) http.Handler {\n','''func (s *Server) requestAuditMiddleware(next http.Handler) http.Handler {
    // Public gateway never journals request/response bodies in the workbench database.
    if s.cfg.Facade.Gateway.Enabled { return next }
''')
replace(p,'r.Body = io.NopCloser(bytes.NewReader(bodyBytes))','r.Body = &auditBody{Reader: io.MultiReader(bytes.NewReader(bodyBytes), r.Body), Closer: r.Body}')
replace(p,'type statusResponseWriter struct {','''type auditBody struct { io.Reader; io.Closer }

func (w *statusResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusResponseWriter) FlushError() error { return http.NewResponseController(w.ResponseWriter).Flush() }

type statusResponseWriter struct {''')
replace(p,'func (s *Server) Close() error {\n','''func (s *Server) Close() error {
    if s.gateway != nil { defer s.gateway.Close() }
    if s.runner != nil { defer s.runner.Close() }
''')

p='internal/middleware/middleware.go'
replace(p,'func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }','func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }\nfunc (s *statusRecorder) FlushError() error { return http.NewResponseController(s.ResponseWriter).Flush() }')

p='internal/facade/types.go'
replace(p,'\t"encoding/json"','\t"encoding/json"\n\t"errors"')
start=load(p).index('func decodeJSON[T any](')
files[p]=load(p)[:start]+'''func decodeJSON[T any](body []byte) (*T, map[string]json.RawMessage, error) {
    body = bytes.TrimSpace(body)
    if len(body)==0 || body[0]!='{' { return nil,nil,errors.New("request must be one JSON object") }
    var raw map[string]json.RawMessage
    if err:=json.Unmarshal(body,&raw);err!=nil { return nil,nil,err }
    var typed T
    if err:=json.Unmarshal(body,&typed);err!=nil { return nil,nil,err }
    return &typed,raw,nil
}
'''
p='internal/facade/chat.go'
replace(p,'func (h *Handler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {\n','''func (h *Handler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
    if r.Header.Get("X-Local-Workspace")!="" { writeError(w,400,"invalid_request_error","HTTP clients cannot write server-local workspace files"); return }
''')
regex(p,r'\n\t\tif localWorkspace := r.Header.Get\("X-Local-Workspace"\); localWorkspace != "" \{.*?\n\t\t\}', '', count=0) if False else None
old='''\t\tif localWorkspace := r.Header.Get("X-Local-Workspace"); localWorkspace != "" {
\t\t\tif err := ApplyLocalWorkspaceFiles(localWorkspace, res.DeltaFiles); err != nil {
\t\t\t\th.log.Warn("本地工作区文件写入异常", "path", localWorkspace, "err", err)
\t\t\t} else {
\t\t\t\th.log.Info("已成功将文件变更同步写入本地工作区", "path", localWorkspace, "files", len(res.DeltaFiles))
\t\t\t}
\t\t}'''
replace(p,old,'',2)
replace(p,'\treturn cjk + other/4','\treturn cjk + (other+3)/4')

p='internal/prism/types.go'
replace(p,'type Usage struct {\n','''type Usage struct {
    Invalid bool `json:"-"`
    CachedTokens *int `json:"-"`
    ReasoningTokens *int `json:"-"`
''')
p='internal/prism/client.go'
replace(p,'if up.MaxRetries <= 0 {','if up.MaxRetries < 0 {')
replace(p,'\tfor attempt := 0; attempt <= c.up.MaxRetries; attempt++ {','''    // Non-idempotent POSTs (start/project/upload) must not be replayed after
    // an unknown transport outcome. Retrying a status lookup is safe.
    maxRetries := c.up.MaxRetries
    if method != http.MethodGet && method != http.MethodHead && stripQuery(path) != c.schema.StatusPath { maxRetries = 0 }
\tfor attempt := 0; attempt <= maxRetries; attempt++ {''')
replace(p,'if attempt == c.up.MaxRetries {','if attempt == maxRetries {')
replace(p,'attempt < c.up.MaxRetries && bodyReader == nil','attempt < maxRetries && bodyReader == nil')
regex(p,r'(?P<i>\t+)out\.Usage = &Usage\{\n\s*InputTokens:\s*intOf\((u|um), "input_tokens", "prompt_tokens"\),\n\s*OutputTokens:\s*intOf\(\2, "output_tokens", "completion_tokens"\),\n\s*TotalTokens:\s*intOf\(\2, "total_tokens"\),\n\s*\}\n\s*if out\.Usage.TotalTokens == 0 \{\n\s*out\.Usage.TotalTokens = out\.Usage.InputTokens \+ out\.Usage.OutputTokens\n\s*\}',lambda m:m.group('i')+'out.Usage = parseUsageMap('+m.group(2)+')',3)

p='internal/facade/runner.go'
replace(p,'\t"strings"','\t"strings"\n\t"sync"')
replace(p,'type RunRequest struct {\n','''type RunRequest struct {
    Isolated bool
    OnAccepted func(context.Context) error
''')
replace(p,'type RunResult struct {\n','type RunResult struct {\n\tUsageEstimated bool\n')
replace(p,'type Runner struct {\n','type Runner struct {\n\tstopGC context.CancelFunc\n\taccountGates sync.Map\n')
replace(p,'\tgo r.projects.gc(context.Background())\n\tgo r.sandboxes.gc(context.Background())','\tgcCtx,cancelGC:=context.WithCancel(context.Background())\n\tr.stopGC=cancelGC\n\tgo r.projects.gc(gcCtx)\n\tgo r.sandboxes.gc(gcCtx)')
replace(p,'\t\tfor range ticker.C {\n\t\t\tr.journal.Cleanup(1 * time.Hour)\n\t\t}','\t\tfor { select { case <-gcCtx.Done(): return; case <-ticker.C: r.journal.Cleanup(1 * time.Hour) } }')
replace(p,'// ProjectCacheSize 供指标使用。','// Close stops runner-owned maintenance goroutines.\nfunc (r *Runner) Close() { if r.stopGC!=nil { r.stopGC() } }\n\n// ProjectCacheSize 供指标使用。')
replace(p,'func (r *Runner) Run(ctx context.Context, req *RunRequest, emit func(Delta) error) (*RunResult, error) {\n','''func (r *Runner) Run(ctx context.Context, req *RunRequest, emit func(Delta) error) (*RunResult, error) {
    copied:=*req;req=&copied
    limit:=r.cfg.Facade.MaxPollTimeout
    if emit==nil && r.cfg.Facade.SyncTimeout>0 && (limit<=0 || r.cfg.Facade.SyncTimeout<limit) { limit=r.cfg.Facade.SyncTimeout }
    if req.Deadline>0 && (limit<=0 || req.Deadline<limit) { limit=req.Deadline }
    if limit>0 { var cancel context.CancelFunc;ctx,cancel=context.WithTimeout(ctx,limit);defer cancel() }
    if err:=ctx.Err();err!=nil { return nil,err }
''')
replace(p,'\tfor attempt := 0; attempt < r.accountRetries; attempt++ {','''    if emit!=nil {
        downstream:=emit
        emit=func(d Delta)error {
            if d.Reset && emitted { return errors.New("upstream rewrote already streamed output") }
            if d.Text!="" || d.Reasoning!="" { emitted=true }
            if err:=downstream(d);err!=nil { return fmt.Errorf("%w: %w",ErrClientGone,err) }
            return nil
        }
    }
\tfor attempt := 0; attempt < r.accountRetries; attempt++ {''')
replace(p,'res, err := r.runOnce(ctx, lease.Account, req, emit)','res, err := r.runLeased(ctx, lease.Account, req, emit)')
replace(p,'if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {','if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrClientGone) || errors.Is(err, account.ErrNoAccount) {')
replace(p,'if res != nil && res.Text != "" {\n\t\t\temitted = true','if res != nil && (res.Text != "" || res.Reasoning != "" || res.RequestID != "") {\n\t\t\temitted = true')
replace(p,'// acquire 选账号。','''func (r *Runner) runLeased(ctx context.Context, acct *account.Account, req *RunRequest, emit func(Delta)error)(*RunResult,error) {
    if req.Isolated {
        value,_:=r.accountGates.LoadOrStore(acct.ID,make(chan struct{},1));gate:=value.(chan struct{})
        select { case gate<-struct{}{}: defer func(){<-gate}(); case <-ctx.Done(): return nil,ctx.Err(); default:return nil,account.ErrNoAccount }
        r.sandboxes.Invalidate(acct.ID)
        defer r.sandboxes.Invalidate(acct.ID)
    }
    return r.runOnce(ctx,acct,req,emit)
}

// acquire 选账号。''')
replace(p,'\t\t\tr.app.AccountPick.Inc("pinned_busy")\n\t\t}\n\t}','\t\t\tr.app.AccountPick.Inc("pinned_busy")\n\t\t}\n\t\treturn nil,account.ErrNoAccount\n\t}')
replace(p,'\tlease, err := r.pool.Acquire(ctx, req.StickyKey)\n\tif err != nil {','\tlease, err := r.pool.Acquire(ctx, req.StickyKey)\n\tif err != nil {\n\t\tif ctx.Err()!=nil { return nil,ctx.Err() }; if req.Isolated { return nil,account.ErrNoAccount }')
replace(p,'\t\tif err != nil {\n\t\t\t// 上下文取消是**终态**','\t\tif err != nil {\n\t\t\tif req.Isolated { return result,err }\n\t\t\t// 上下文取消是**终态**')
replace(p,'\tif projectID != "" {\n\t\tinputItems = preprocessInputImages(ctx, r.client, p, projectID, inputItems)\n\t}','''    if req.Isolated {
        var imageErr error
        inputItems,imageErr=preprocessGatewayImages(ctx,r.client,p,projectID,inputItems)
        if imageErr!=nil { return result,imageErr }
    } else if projectID!="" { inputItems=preprocessInputImages(ctx,r.client,p,projectID,inputItems) }
''')
replace(p,'\tresult.RequestID = requestID\n\tresult.ConversationID = convID','''\tresult.RequestID = requestID
\tresult.ConversationID = convID
    finished:=false
    defer func(){if !finished && requestID!="" {r.stopUpstream(p,requestID,convID,turnState)}}()''')
replace(p,'\tbumpFirstByte := func() {','''    if req.OnAccepted!=nil && (requestID!="" || (startResp.Initial!=nil && startResp.Initial.Done)) && (startResp.Initial==nil || !startResp.Initial.Fail) {
        if err:=req.OnAccepted(ctx);err!=nil { return result,fmt.Errorf("%w: %w",ErrClientGone,err) }
    }
\tbumpFirstByte := func() {''')
replace(p,'\tif st := startResp.Initial; st != nil {','\tif st := startResp.Initial; st != nil {\n\t\tresult.ResponseID=st.ResponseID\n\t\tresult.Reasoning=st.Reasoning')
replace(p,'\t\tif st.Fail {\n\t\t\tr.app.ConversationOps','\t\tif st.Fail {\n\t\t\tfinished=true\n\t\t\tr.app.ConversationOps')
replace(p,'\t\tif st.Done {','\t\tif st.Done {\n\t\t\tfinished=true',2)
replace(p,'\t\t\tif result.Usage == nil {','\t\t\tif result.Usage == nil {\n\t\t\t\tresult.UsageEstimated=true',2)
replace(p,'\t\tif st.Delta != "" {','\t\tif st.Delta != "" || st.ReasoningDelta != "" {')
replace(p,'\tif !f.ReuseProject {','\tif req.Isolated || !f.ReuseProject {')
regex(p,r'^\t{3,5}r\.stopUpstream\(p, requestID, convID, turnState\)\n','',4)
# Request-bound journal writes are disabled for gateway calls. Operator workbench
# journaling remains unchanged, and maintenance Cleanup runs independently.
regex(p,r'^(\s*)(r\.journal\.(?:RecordStart|UpdateState|MarkTerminal)\([^\n]+\))$',lambda m:m.group(1)+'if !req.Isolated { '+m.group(2)+' }',7)
replace(p,'if len(turnState) > 0 {\n\t\tvar tsMap','if !req.Isolated && len(turnState) > 0 {\n\t\tvar tsMap')

p='internal/gateway/stream.go'
replace(p,' terminal bool\n',' terminal bool\n beforeTerminal func(map[string]any)error\n')
replace(p,' response:=responseJSON(s.q,s.id,s.created,status,output,usage)\n',' response:=responseJSON(s.q,s.id,s.created,status,output,usage)\n if s.beforeTerminal!=nil { if err:=s.beforeTerminal(response);err!=nil{return nil,err} }\n')

for path,content in files.items():
    Path(path).write_text(content,encoding='utf-8')
print('Integrated',len(files),'files:',', '.join(sorted(files)))
