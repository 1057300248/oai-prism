"""One-shot integration on the dedicated feature branch. No production access.
All exact edits preflight in memory before any implementation file is written.
"""
from pathlib import Path

files = {}
def edit(path, old, new, count=1):
    text = files.get(path, Path(path).read_text(encoding='utf-8'))
    if text.count(old) != count:
        raise RuntimeError(f'{path}: expected {count} matches, found {text.count(old)}: {old[:100]!r}')
    files[path] = text.replace(old, new)

p='internal/gateway/types.go'
edit(p,'type Options struct {\n','type Options struct {\n\tBridge BridgePolicy `yaml:"bridge"`\n')
edit(p,'func (o Options) Validate() error {\n','func (o Options) Validate() error {\n\tif err:=o.validateBridge();err!=nil{return err}\n')
edit(p,'type Request struct {\n','''type Request struct {
    Bridge BridgePolicy `json:"-"`
    UpstreamState *UpstreamState `json:"-"`
    ContinuationOffset int `json:"-"`
    StoredConversation bool `json:"-"`
''')
edit(p,'type Result struct {\n','type Result struct {\n\tUpstreamState *UpstreamState `json:"-"`\n')
edit(p,'type APIError struct {\n','type APIError struct {\n\tStage string\n')
edit(p,'\tif api.Context != nil {','\tif api.Stage!="" { body["x_oaiprism_stage"]=api.Stage }\n\tif api.Context != nil {')

p='internal/gateway/request.go'
edit(p,'\tq := &Request{CodexTools: o.CodexTools,','\tif err:=normalizeAdditionalTools(m,responses,o);err!=nil{return nil,err}\n\tq := &Request{Bridge:o.Bridge, CodexTools: o.CodexTools,')

p='internal/gateway/context_cache.go'
edit(p,'const renderVersion = "gateway-transcript-v4-media"','const renderVersion = "gateway-transcript-v5-bridge"')
edit(p,'func RenderInput(q *Request) []RenderMessage {','func RenderInput(q *Request) []RenderMessage { return renderInputWindow(q,0) }\n\nfunc renderInputWindow(q *Request, start int) []RenderMessage {')
edit(p,'\tfor _, item := range CanonicalItems(q.Items) {','\tfor itemIndex, item := range CanonicalItems(q.Items) {')
edit(p,'\t\tfor i, part := range item.Content {','\t\tif itemIndex<start {continue}\n\t\tfor i, part := range item.Content {')
edit(p,'\tparts := []Content{{Type: "input_text", Text: transcript.String()}}','''    text:=transcript.String()
    if start>0 {text="Only new entries for the active upstream conversation follow. Retain earlier history.\\n"+text}
    if q.Bridge.InstructionPlacement=="user_relay" && system!="" {
        // A compatibility duplication, not deletion or silent truncation of the
        // proper system role. The selected profile is part of cache fingerprints.
        encoded,_:=json.Marshal(system)
        text="[Transport instruction copy; JSON string follows]\\n"+string(encoded)+"\\n[Conversation data follows]\\n"+text
    }
\tparts := []Content{{Type: "input_text", Text: text}}''')
edit(p,'}{renderVersion, q.Model, route,','}{renderVersion+":"+q.Bridge.InstructionPlacement, q.Model, route,')

p='internal/gateway/handler.go'
edit(p,'type Handler struct {\n','type Handler struct {\n\tcontinuations *continuationRegistry\n')
edit(p,'h := &Handler{options: o, engine: engine, summaries: newSummaryCache()}','h := &Handler{options: o, engine: engine, summaries: newSummaryCache(), continuations:newContinuationRegistry()}')
edit(p,'func (h *Handler) Close() error {\n','func (h *Handler) Close() error {\n\th.continuations.clear()\n')
edit(p,'\tcase path == "/v1/capabilities":','''    case path == "/v1/transport-status":
        if r.Method!="GET" {h.method(w,"GET");return}
        report:=bridgeStatus(h.options);report["continuation"]=h.continuations.stats();writeJSON(w,200,report);return
\tcase path == "/v1/capabilities":''')
edit(p,'\t\t\twriteJSON(w, 200, map[string]any{"id": id, "object": "response.deleted", "deleted": true})','\t\t\th.continuations.revoke(owner,id)\n\t\t\twriteJSON(w, 200, map[string]any{"id": id, "object": "response.deleted", "deleted": true})')
edit(p,'\tapi := publicError(err)\n','\tapi := publicError(err)\n\tif api.Stage!="" { w.Header().Set("X-Oaiprism-Stage",api.Stage) }\n')
edit(p,'\tresult, err := h.engine.Run(ctx, q, accepted, emit)','''    lease,err:=h.beginContinuation(q,owner,id,r.Header)
    if err!=nil {h.fail(w,r,s,withContextReport(q,err));return}
    defer lease.close()
    mode:="stateless";if lease!=nil {mode="new-stored-conversation"};if q.UpstreamState!=nil {mode="delta"}
    w.Header().Set("X-Oaiprism-Continuation",mode)
    if err=CheckTransportBudget(q);err!=nil {h.fail(w,r,s,withContextReport(q,err));return}
\tresult, err := h.engine.Run(ctx, q, accepted, emit)''')
edit(p,'\t\t_, err = s.finish(result, usage)\n\t\tif err != nil {','\t\t_, err = s.finish(result, usage)\n\t\tif err == nil {lease.complete(q,result,history)}\n\t\tif err != nil {')
edit(p,'\twriteJSON(w, 200, response)\n}','\tif err=writeGeneratedJSON(w,200,response);err==nil {lease.complete(q,result,history)}\n}')

p='internal/gateway/stream.go'
edit(p,'response["error"] = map[string]any{"code": api.Code, "message": api.Message}','response["error"] = map[string]any{"code": api.Code, "message": api.Message}\n\t\tif api.Stage!="" { response["x_oaiprism_stage"]=api.Stage }')

p='internal/facade/gateway_adapter.go'
edit(p,'out := &gateway.Result{Text: result.Text}','out := &gateway.Result{Text: result.Text, UpstreamState:result.GatewayState}')
edit(p,'rendered := gateway.RenderInput(q)','rendered := gateway.RenderUpstreamInput(q)')
edit(p,'API: "gateway", Isolated: true, Extra: q.NativeCacheFields()}','API: "gateway", Isolated: true, Extra: q.NativeCacheFields(), GatewayStateful:q.StoredConversation, GatewayResume:q.UpstreamState, GatewayActionID:q.Bridge.Continuation.ActionID, GatewayMaxStartBytes:q.Bridge.MaxStartBytes}')
edit(p,'\tif q.CacheAffinity {','\tif q.UpstreamState!=nil {run.AccountID=q.UpstreamState.AccountID}\n\tif q.CacheAffinity {')
start='''\tif err != nil {
\t\tif errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
\t\t\treturn nil, err
\t\t}
\t\tif errors.Is(err, ErrPollTimeout) {
\t\t\treturn nil, context.DeadlineExceeded
\t\t}
\t\tif errors.Is(err, account.ErrNoAccount) {
\t\t\treturn nil, &gateway.APIError{Status: 503, Code: "upstream_unavailable", Message: "No upstream account is currently available."}
\t\t}
\t\tvar api *creds.APIError
\t\tif errors.As(err, &api) && api.Status == 429 {
\t\t\treturn nil, &gateway.APIError{Status: 429, Code: "rate_limit_exceeded", Message: "The upstream is rate limited. Retry later.", RetryAfter: api.RetryAfter}
\t\t}
\t\treturn nil, errors.New("upstream execution failed") // Never expose upstream bodies, tokens or filesystem paths.
\t}'''
edit(p,start,'''    if err!=nil {
        stage:="account"
        var staged *gatewayStageError
        if errors.As(err,&staged) && staged.stage!="" {stage=staged.stage}
        var public *gateway.APIError
        if errors.As(err,&public) {copy:=*public;if copy.Stage==""{copy.Stage=stage};return nil,&copy}
        var size *prism.StartSizeError
        if errors.As(err,&size){return nil,&gateway.APIError{Status:400,Code:"context_length_exceeded",Param:"input",Stage:"transport_budget",Message:"The serialized request exceeds the configured upstream byte limit; no generation was started."}}
        if errors.Is(err,context.Canceled){return nil,&gateway.APIError{Status:499,Code:"request_cancelled",Stage:stage,Message:"The request was cancelled."}}
        if errors.Is(err,context.DeadlineExceeded)||errors.Is(err,ErrPollTimeout){return nil,&gateway.APIError{Status:504,Code:"upstream_timeout",Stage:stage,Message:"The upstream request exceeded its deadline."}}
        if errors.Is(err,account.ErrNoAccount){return nil,&gateway.APIError{Status:503,Code:"upstream_unavailable",Stage:"account",Message:"No eligible upstream account is available."}}
        var api *creds.APIError
        if errors.As(err,&api)&&api.Status==429{return nil,&gateway.APIError{Status:429,Code:"rate_limit_exceeded",Stage:stage,Message:"The upstream is rate limited. Retry later.",RetryAfter:api.RetryAfter}}
        return nil,&gateway.APIError{Status:502,Code:"upstream_error",Stage:stage,Message:"The upstream request failed. Refer to x-request-id and x_oaiprism_stage."}
    }''')
edit(p,'projectID string, items []prism.InputItem) ([]prism.InputItem, error) {','projectID string, items []prism.InputItem, modes ...string) ([]prism.InputItem, error) {\n\tmode:="multipart";if len(modes)>0 {mode=modes[0]}')
edit(p,'if _, err = client.UploadFile(ctx, p, prism.FileUpload{ProjectID: projectID, Path: name, Filename: name, Data: data}); err != nil {','projectPath,err:=client.UploadGatewayFile(ctx,p,prism.FileUpload{ProjectID:projectID,Path:name,Filename:name,Data:data},mode)\n\t\t\t\tif err!=nil {')
edit(p,'if _, err := client.UploadFile(ctx, p, prism.FileUpload{ProjectID: projectID, Path: name, Filename: name, Data: data}); err != nil {','projectPath,err:=client.UploadGatewayFile(ctx,p,prism.FileUpload{ProjectID:projectID,Path:name,Filename:name,Data:data},mode)\n\t\t\tif err!=nil {')
edit(p,'prism.InputContent{Type: "input_file", Filename: name, ProjectPath: name}','prism.InputContent{Type:"input_file",Filename:name,ProjectPath:projectPath}',2)

p='internal/facade/runner.go'
edit(p,'\t"github.com/oai-prism/oaiprism/internal/config"','\t"github.com/oai-prism/oaiprism/internal/config"\n\t"github.com/oai-prism/oaiprism/internal/gateway"')
edit(p,'type RunRequest struct {\n','''type RunRequest struct {
    GatewayStateful bool
    GatewayResume *gateway.UpstreamState
    GatewayEpoch uint64
    GatewayActionID string
    GatewayMaxStartBytes int
''')
edit(p,'type RunResult struct {\n','type RunResult struct {\n\tGatewayState *gateway.UpstreamState\n\tRunStage string\n')
edit(p,'type Runner struct {\n','type Runner struct {\n\taccountEpoch sync.Map\n')
edit(p,'\t\tr.pool.MarkResult(lease.Account, err, retryAfter(err))','\t\tr.pool.MarkResult(lease.Account, err, retryAfter(err))\n\t\tif req.GatewayStateful {return res,err}')
edit(p,'\t\tr.sandboxes.Invalidate(acct.ID)\n\t\tdefer r.sandboxes.Invalidate(acct.ID)','\t\tif err:=r.fenceGatewayAccount(acct,req);err!=nil{return nil,err}\n\t\tr.sandboxes.Invalidate(acct.ID)\n\t\tdefer r.sandboxes.Invalidate(acct.ID)')
edit(p,'\treturn r.runOnce(ctx, acct, req, emit)','\tres,err:=r.runOnce(ctx,acct,req,emit)\n\tif req.Isolated&&err!=nil {stage:="setup";if res!=nil&&res.RunStage!=""{stage=res.RunStage};return res,&gatewayStageError{stage,err}}\n\treturn res,err')
edit(p,'func (r *Runner) acquire(ctx context.Context, req *RunRequest) (*account.Lease, error) {\n','''func (r *Runner) acquire(ctx context.Context, req *RunRequest) (*account.Lease, error) {
    if req.AccountID!="" && req.AllowedAccounts!=nil {
        allowed:=false;for _,id:=range req.AllowedAccounts{allowed=allowed||id==req.AccountID}
        if !allowed{return nil,account.ErrNoAccount}
        copy:=*req;copy.AllowedAccounts=nil;return r.acquire(ctx,&copy)
    }
''')
edit(p,'\tprojectID := req.ProjectID','\tresult.RunStage="project"\n\tprojectID := req.ProjectID')
edit(p,'\tvar sb *prism.Sandbox\n\tif !req.IsAux {','''    result.RunStage="sandbox"
\tvar sb *prism.Sandbox
    if req.GatewayResume!=nil {
        restored,err:=restoreGatewaySandbox(ctx,r.client,p,req.GatewayResume)
        if err!=nil{return result,err};sb=restored
    } else if !req.IsAux {''')
edit(p,'\t\t\tr.app.SandboxOps.Inc("acquire", "error")','\t\t\tr.app.SandboxOps.Inc("acquire", "error")\n\t\t\tif req.Isolated{return result,err}')
edit(p,'\tif sb.Usable() && projectID != "" {\n\t\tif !r.syncSandboxWorkspace','\tresult.RunStage="workspace_sync"\n\tif req.GatewayResume==nil && sb.Usable() && projectID != "" {\n\t\tif !r.syncSandboxWorkspace')
edit(p,'\tinputItems := req.Input','''    result.RunStage="conversation_create"
    if err:=prepareGatewaySnapshot(ctx,r.client,p,req,projectID,sb,meta);err!=nil{return result,err}
    result.RunStage="upload"
\tinputItems := req.Input''')
edit(p,'preprocessGatewayImages(ctx, r.client, p, projectID, inputItems)','preprocessGatewayImages(ctx, r.client, p, projectID, inputItems,r.cfg.Facade.Gateway.Bridge.UploadMode)')
edit(p,'\tfor attempt := 1; attempt <= sandboxStartRetries; attempt++ {','\tresult.RunStage="start"\n\tfor attempt := 1; attempt <= sandboxStartRetries; attempt++ {')
edit(p,'\t\tstartResp, err = r.client.StartResponse(ctx, p, &prism.StartRequest{','\t\tstartResp, err = r.client.StartResponse(ctx, p, &prism.StartRequest{\n\t\t\tMaxBodyBytes:req.GatewayMaxStartBytes,')
edit(p,'\t\t\tif isSentinelThrottle(err) && attempt < sandboxStartRetries {','\t\t\tif !req.Isolated && isSentinelThrottle(err) && attempt < sandboxStartRetries {')
edit(p,'\t\tif !isSandboxNotReady(startResp) {','\t\tif req.Isolated || !isSandboxNotReady(startResp) {')
edit(p,'\tresult.RequestID = requestID','\tif convID==""&&req.GatewayStateful {convID=req.ConversationID}\n\tresult.RequestID = requestID')
edit(p,'\t\t\treturn result, nil\n\t\t}\n', '\t\t\tresult.GatewayState=captureGatewayState(req,result,sb)\n\t\t\treturn result, nil\n\t\t}\n',2)
edit(p,'\t// 4) 轮询直到终态。','\tresult.RunStage="poll"\n\t// 4) 轮询直到终态。')

p='internal/prism/types.go'
edit(p,'type StartRequest struct {\n','type StartRequest struct {\n\tMaxBodyBytes int\n')
edit(p,'// PreviousResponseID 是上一轮的 request_id。','// PreviousResponseID 是上一轮终态 payload.id，不是轮询用的 request_id。')
p='internal/prism/client.go'
edit(p,'\tpayload := c.buildStartPayload(req)\n','\tpayload := c.buildStartPayload(req)\n\tif err:=checkStartSize(payload,req.MaxBodyBytes);err!=nil{return nil,err}\n')
edit(p,'\tif st, ok := c.parseEnvelope(v, raw, "", ""); ok {','\tif st, ok := c.parseEnvelope(v, raw, "", ""); ok {\n\t\tenrichContinuationStatus(st,raw)')
edit(p,'\tif st, ok := c.parseEnvelope(v, raw, fallbackID, prevText); ok {','\tif st, ok := c.parseEnvelope(v, raw, fallbackID, prevText); ok {\n\t\tenrichContinuationStatus(st,raw)')

p='internal/server/server.go'
edit(p,'\t\tif err := gatewayOptions.Validate(); err != nil {','\t\tif gatewayOptions.Bridge.Continuation.Enabled&&!cfg.Facade.UseSandbox{return nil,fmt.Errorf("upstream continuation requires facade.use_sandbox")}\n\t\tif err := gatewayOptions.Validate(); err != nil {')

# New implementation/tests are staged as text, so intermediate cloud commits do
# not publish a partly-integrated Go build. The final workflow installs them all.
new_files={
 'gateway_bridge.go.txt':'internal/gateway/bridge.go',
 'gateway_continuation.go.txt':'internal/gateway/continuation.go',
 'prism_transport.go.txt':'internal/prism/gateway_transport.go',
 'facade_bridge.go.txt':'internal/facade/gateway_lifecycle.go',
 'gateway_bridge_test.go.txt':'internal/gateway/bridge_test.go',
 'prism_transport_test.go.txt':'internal/prism/gateway_transport_test.go',
 'facade_bridge_test.go.txt':'internal/facade/gateway_lifecycle_test.go',
 'server_bridge_test.go.txt':'internal/server/bridge_e2e_test.go',
}
for source,target in new_files.items():
    if Path(target).exists():raise RuntimeError(f'refusing to overwrite existing {target}')
    text=Path('tools/bridge-upgrade',source).read_text(encoding='utf-8')
    if source=='gateway_continuation.go.txt':
        text=text.replace('type UpstreamState struct {','type UpstreamState struct {\n Epoch uint64 `json:"-"`',1)
    files[target]=text
for path,content in files.items():Path(path).write_text(content,encoding='utf-8')
print('Applied guarded bridge integration:',len(files),'files')
