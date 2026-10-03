"""One-shot integration: preflight every exact edit before touching any file."""
from pathlib import Path
files={}
def edit(path,old,new,n=1):
    text=files.get(path,Path(path).read_text())
    assert text.count(old)==n,(path,text.count(old),old[:100])
    files[path]=text.replace(old,new)

def replace_function(path,start,end,body):
    text=files.get(path,Path(path).read_text())
    a=text.index(start);b=text.index(end,a)
    files[path]=text[:a]+body+'\n\n'+text[b:]

p='internal/gateway/types.go'
edit(p,'\t"github.com/oai-prism/oaiprism/internal/catalog"','\t"github.com/oai-prism/oaiprism/internal/catalog"\n\t"github.com/oai-prism/oaiprism/internal/attachment"')
edit(p,'type Options struct {\n','type Options struct {\n\tFiles attachment.Options `yaml:"files"`\n\tMedia MediaPolicy `yaml:"media"`\n')
edit(p,'func (o Options) Validate() error {\n','''func (o Options) Validate() error {
    if err:=o.Files.Validate();err!=nil{return err}
    if err:=o.Media.validate();err!=nil{return err}
    if o.Files.Enabled && (o.TenantHeader=="" || len(o.TrustedPeers)==0) {return errors.New("files require a trusted tenant header and peer; shared channel keys are not user identities")}
    if o.Files.Path!="" && o.Files.Path==o.StorePath {return errors.New("files and response stores must use separate database paths")}
''')
edit(p,'type Content struct {\n','type Content struct {\n\tFileID string `json:"file_id,omitempty"`\n\tFilename string `json:"filename,omitempty"`\n\tFileData string `json:"file_data,omitempty"`\n')
edit(p,'type Request struct {\n','type Request struct {\n\tFileIDs []string\n\tHasMedia bool\n\tMediaPolicy MediaPolicy\n')
p='internal/gateway/handler.go'
edit(p,'\t"github.com/oai-prism/oaiprism/internal/catalog"','\t"github.com/oai-prism/oaiprism/internal/catalog"\n\t"github.com/oai-prism/oaiprism/internal/attachment"')
edit(p,'type Handler struct {\n','type Handler struct {\n\tfiles *attachment.Store\n')
edit(p,'\treturn h, nil\n}', '''    if o.Files.Enabled {
        h.files,err=attachment.New(o.Files)
        if err!=nil{h.catalog.Close();_ = h.store.Close();return nil,err}
    }
\treturn h, nil
}''')
edit(p,'func (h *Handler) Close() error { h.catalog.Close(); h.summaries.clear(); return h.store.Close() }','func (h *Handler) Close() error { h.catalog.Close(); h.summaries.clear(); if h.files!=nil{_ = h.files.Close()}; return h.store.Close() }')
edit(p,'\tif h.serveCatalog(w, r) {','\tif h.serveFiles(w,r,owner){return}\n\tif h.serveCatalog(w, r) {')
edit(p,'"object": "gateway.capabilities",','"object": "gateway.capabilities", "attachments": h.attachmentCapabilities(),')
edit(p,'\t\tq.Items = append(snapshot.Items, q.Items...)','\t\tq.Items = append(snapshot.Items, q.Items...)\n\t\tq.FileIDs = append([]string(nil),snapshot.FileIDs...)')
edit(p,'\tfor i := range q.Items {\n\t\tif q.Items[i].ID == "" {','\tfor i := range q.Items {\n\t\tif q.Items[i].ID == "" {',n=2) # exact anchor count guard
anchor='\tfor i := range q.Items {\n\t\tif q.Items[i].ID == "" {'
text=files[p];pos=text.index(anchor,text.index('func (h *Handler) generate'))
files[p]=text[:pos]+'\tif err:=h.resolveAttachments(ctx,q,owner);err!=nil{h.fail(w,r,nil,err);return}\n'+text[pos:]
edit(p,'Snapshot{Response: raw, Items: history, InputItems: append([]Item(nil), q.Items...)}','Snapshot{Response: raw, Items: history, InputItems: append([]Item(nil), q.Items...), FileIDs: append([]string(nil),q.FileIDs...)}')
p='internal/gateway/store.go'
edit(p,'type Snapshot struct {\n','type Snapshot struct {\n\tFileIDs []string `json:"file_ids,omitempty"`\n')
p='internal/gateway/context_endpoint.go'
edit(p,'\t\tq.Items = append(snapshot.Items, q.Items...)','\t\tq.Items = append(snapshot.Items, q.Items...)\n\t\tq.FileIDs = append([]string(nil),snapshot.FileIDs...)')
edit(p,'\traw, _ := json.Marshal(q.Items)','\tif err:=h.resolveAttachments(ctx,q,owner);err!=nil{h.fail(w,r,nil,err);return}\n\traw, _ := json.Marshal(q.Items)')
edit(p,'"x_oaiprism_tokenizer": "o200k_base_rendered"','"x_oaiprism_tokenizer": "o200k_base_rendered", "x_oaiprism_media_budget": q.HasMedia')
p='internal/gateway/request.go'
edit(p,'\t"encoding/base64"\n','')
edit(p,'\t"net/http"\n','')
edit(p,'\t\tswitch typ {\n\t\tcase "text", "input_text", "output_text":','''        if part,handled,e:=parseAttachmentBlock(m,role,o);handled {
            if e!=nil{return nil,e};out=append(out,part);continue
        }
\t\tswitch typ {
\t\tcase "text", "input_text", "output_text":''')
replace_function(p,'func validateImage(url string) error {','// ValidateHistory', 'func validateImage(url string) error { _,_,err:=imageData(url);return err }')
p='internal/gateway/context_cache.go'
edit(p,'const renderVersion = "gateway-transcript-v3"','const renderVersion = "gateway-transcript-v4-media"')
edit(p,'\t\t\tif part.Type == "input_image" {\n\t\t\t\timages = append(images, part)','\t\t\tif part.Type == "input_image" || part.Type == "input_file" {\n\t\t\t\timages = append(images, part)')
edit(p,'Text: "[inline image attached]"','Text: "[binary attachment " + strconv.Itoa(len(images)) + " attached: " + part.Filename + "]"')
replace_function(p,'func RenderedTokens(q *Request) (int, error) {','func (h *Handler) bindPromptCache','func RenderedTokens(q *Request) (int, error) { return RenderedMediaTokens(q) }')
p='internal/gateway/context_compaction.go'
edit(p,'\tfor _, cut := range cuts {\n\t\tif cut <= protect {','''    // Binary evidence must remain attached, not be encoded as text for a summary.
    // Retain its entire turn and every later turn. Compaction may then fail closed
    // if this indivisible suffix alone exceeds the configured input budget.
    lastUser:=0
    for i,it:=range history {
        if it.Type=="message" && it.Role=="user" {lastUser=i}
        for _,part:=range it.Content {if part.Type=="input_image" || part.Type=="input_file" {if lastUser<protect{protect=lastUser}}}
    }
\tfor _, cut := range cuts {
\t\tif cut <= protect {''')
p='internal/gateway/semantics.go'
edit(p,'\tu := result.Usage\n\tif u == nil || u.Source != "upstream" {','\tu := result.Usage\n\tif q.HasMedia && (u==nil || u.Source!="upstream") {return nil,errors.New("binary-media billing requires authoritative upstream usage")}\n\tif u == nil || u.Source != "upstream" {')
p='internal/prism/types.go'
edit(p,'type InputContent struct {\n','type InputContent struct {\n\t// Gateway-only transient data; must be uploaded before encoding the start request.\n\tGatewayFileData string `json:"-"`\n')
p='internal/facade/gateway_adapter.go'
edit(p,'\t"context"','\t"context"\n\t"encoding/base64"')
edit(p,'ImageURL: part.ImageURL, Detail: part.Detail','ImageURL: part.ImageURL, Detail: part.Detail, Filename: part.Filename, GatewayFileData: part.FileData')
edit(p,'\t\tfor j, part := range item.Content {\n\t\t\tif part.Type != "input_image" {','''\t\tfor j, part := range item.Content {
            if part.Type=="input_file" {
                if projectID=="" || part.GatewayFileData=="" {return nil,errors.New("PDF requires an isolated project and resolved data")}
                data,err:=base64.StdEncoding.Strict().DecodeString(part.GatewayFileData)
                if err!=nil || len(data)>8<<20 || !strings.HasPrefix(string(data),"%PDF-"){return nil,errors.New("invalid gateway PDF")}
                name:="gateway_pdf_"+randHex(12)+".pdf"
                if _,err=client.UploadFile(ctx,p,prism.FileUpload{ProjectID:projectID,Path:name,Filename:name,Data:data});err!=nil{return nil,err}
                output[i].Content[j]=prism.InputContent{Type:"input_file",Filename:name,ProjectPath:name}
                continue
            }
\t\t\tif part.Type != "input_image" {''')
for path,text in files.items():Path(path).write_text(text)
print('Integrated',len(files),'files')
