package gateway

import (
 "context"
 "encoding/base64"
 "encoding/json"
 "errors"
 "io"
 "mime"
 "net/http"
 "strconv"
 "strings"
 "time"

 "github.com/oai-prism/oaiprism/internal/attachment"
)

func fileError(err error)error{
 if errors.Is(err,attachment.ErrNotFound){return &APIError{Status:404,Code:"file_not_found",Param:"file_id",Message:"File not found, expired, deleted, or not owned by this caller."}}
 if errors.Is(err,attachment.ErrQuota){return &APIError{Status:429,Code:"file_quota_exceeded",Message:"File quota exhausted. Delete unused files or wait for expiry."}}
 return err
}
func(h *Handler)serveFiles(w http.ResponseWriter,r *http.Request,owner string)bool{
 path:=r.URL.Path
 if path!="/v1/files"&&!strings.HasPrefix(path,"/v1/files/"){return false}
 if h.files==nil{h.fail(w,r,nil,&APIError{Status:404,Code:"not_found",Message:"Files API is disabled."});return true}
 select{case h.slots<-struct{}{}:defer func(){<-h.slots}();default:h.fail(w,r,nil,&APIError{Status:503,Code:"gateway_busy",Message:"Gateway capacity exhausted."});return true}
 ctx,cancel:=context.WithTimeout(r.Context(),30*time.Second);defer cancel()
 if ce:=r.Header.Get("Content-Encoding");ce!=""&&!strings.EqualFold(ce,"identity"){h.fail(w,r,nil,&APIError{Status:415,Code:"unsupported_content_encoding",Message:"Send uncompressed file data."});return true}
 if path=="/v1/files"{
  switch r.Method{
  case "POST":f,err:=h.uploadFile(w,r.WithContext(ctx),owner);if err!=nil{h.fail(w,r,nil,fileError(err));return true};writeJSON(w,200,f)
  case "GET":page,err:=h.listFiles(ctx,owner,r);if err!=nil{h.fail(w,r,nil,fileError(err));return true};writeJSON(w,200,page)
  default:h.method(w,"GET, POST")
  };return true
 }
 id,tail,_:=strings.Cut(strings.TrimPrefix(path,"/v1/files/"),"/")
 if id==""||len(id)>128||(tail!=""&&tail!="content"){h.fail(w,r,nil,fileError(attachment.ErrNotFound));return true}
 if len(r.URL.Query())>0{h.fail(w,r,nil,unsupported("query"));return true}
 if r.Method=="DELETE"&&tail==""{
  if err:=h.files.Delete(ctx,id,owner);err!=nil{h.fail(w,r,nil,fileError(err));return true}
  writeJSON(w,200,map[string]any{"object":"file","id":id,"deleted":true});return true
 }
 if r.Method!="GET"{allow:="GET";if tail==""{allow="GET, DELETE"};h.method(w,allow);return true}
 f,err:=h.files.Get(ctx,id,owner);if err!=nil{h.fail(w,r,nil,fileError(err));return true}
 if tail==""{writeJSON(w,200,f);return true}
 // Always download, even HTML/SVG-like text. Never render uploaded active content.
 w.Header().Set("Content-Type","application/octet-stream")
 w.Header().Set("Content-Disposition",mime.FormatMediaType("attachment",map[string]string{"filename":f.Filename}))
 w.Header().Set("Cache-Control","no-store")
 w.Header().Set("X-Content-Type-Options","nosniff")
 w.Header().Set("Content-Security-Policy","sandbox; default-src 'none'")
 w.Header().Set("Content-Length",strconv.Itoa(len(f.Data)))
 _ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30*time.Second))
 defer http.NewResponseController(w).SetWriteDeadline(time.Time{})
 _,_=w.Write(f.Data);return true
}
func(h *Handler)uploadFile(w http.ResponseWriter,r *http.Request,owner string)(attachment.File,error){
 empty:=attachment.File{}
 if len(r.URL.Query())!=0{return empty,unsupported("query")}
 r.Body=http.MaxBytesReader(w,r.Body,attachment.MaxFileBytes+(64<<10));defer r.Body.Close()
 reader,err:=r.MultipartReader();if err!=nil{return empty,bad("file","Expected multipart/form-data.")}
 fields:=map[string]string{};var data []byte;name:="";count:=0
 for{
  part,e:=reader.NextPart();if e==io.EOF{break};if e!=nil{var large *http.MaxBytesError;if errors.As(e,&large){return empty,&APIError{Status:413,Code:"request_too_large",Message:"Upload exceeds the gateway limit."}};return empty,bad("file","Malformed multipart body.")}
  count++;if count>5{_ = part.Close();return empty,bad("file","Too many multipart fields.")}
  _,params,e:=mime.ParseMediaType(part.Header.Get("Content-Disposition"));if e!=nil{_ = part.Close();return empty,bad("file","Invalid multipart disposition.")}
  field:=part.FormName()
  if _,exists:=fields[field];exists{_ = part.Close();return empty,bad(field,"Duplicate multipart field.")};fields[field]=""
  limit:=int64(1024)
  if field=="file"{limit=attachment.MaxFileBytes;name=params["filename"];if e=checkedFilename(name);e!=nil{_ = part.Close();return empty,e}}else if field!="purpose"&&field!="expires_after[anchor]"&&field!="expires_after[seconds]"{_ = part.Close();return empty,unsupported(field)}
  value,e:=io.ReadAll(io.LimitReader(part,limit+1));_ = part.Close()
  if e!=nil||int64(len(value))>limit{return empty,&APIError{Status:413,Code:"request_too_large",Message:"Multipart field exceeds its gateway limit."}}
  if field=="file"{data=value}else{fields[field]=string(value)}
 }
 if err=r.Context().Err();err!=nil{return empty,err}
 if name==""||len(data)==0{return empty,bad("file","Exactly one non-empty uploaded file is required.")}
 if fields["purpose"]!="user_data"{return empty,unsupported("purpose")}
 ttl:=time.Duration(0);anchor,hasAnchor:=fields["expires_after[anchor]"];seconds,hasSeconds:=fields["expires_after[seconds]"]
 if hasAnchor!=hasSeconds{return empty,bad("expires_after","Specify both anchor and seconds.")}
 if hasAnchor{n,e:=strconv.Atoi(seconds);if e!=nil||anchor!="created_at"||n<3600||n>2592000{return empty,bad("expires_after","Expiry must be created_at plus 3600..2592000 seconds.")};ttl=time.Duration(n)*time.Second;if h.options.Files.TTL>0&&ttl>h.options.Files.TTL{return empty,bad("expires_after","Expiry exceeds the operator retention policy.")};if h.options.Files.TTL==0&&ttl>24*time.Hour{return empty,bad("expires_after","Expiry exceeds the default 24-hour retention policy.")}}
 contentType,err:=classifyAttachment(name,data);if err!=nil{return empty,err}
 return h.files.Put(r.Context(),owner,name,fields["purpose"],contentType,data,ttl)
}
func(h *Handler)listFiles(ctx context.Context,owner string,r *http.Request)(map[string]any,error){
 query:=r.URL.Query();for key,values:=range query{if len(values)!=1{return nil,bad(key,"Duplicate query parameter.")};if key!="purpose"&&key!="after"&&key!="limit"&&key!="order"{return nil,unsupported(key)}}
 if p:=query.Get("purpose");p!=""&&p!="user_data"{return nil,unsupported("purpose")}
 limit:=100;if v:=query.Get("limit");v!=""{n,e:=strconv.Atoi(v);if e!=nil||n<1||n>10000{return nil,bad("limit","Expected limit 1..10000.")};limit=n}
 order:=query.Get("order");if order==""{order="desc"};if order!="asc"&&order!="desc"{return nil,bad("order","Expected asc or desc.")}
 files,err:=h.files.List(ctx,owner);if err!=nil{return nil,err}
 if order=="desc"{for i,j:=0,len(files)-1;i<j;i,j=i+1,j-1{files[i],files[j]=files[j],files[i]}}
 if after:=query.Get("after");after!=""{index:=-1;for i,f:=range files{if f.ID==after{index=i;break}};if index<0{return nil,bad("after","Cursor not found in this caller's live file list.")};files=files[index+1:]}
 more:=len(files)>limit;if more{files=files[:limit]};var first,last any;if len(files)>0{first=files[0].ID;last=files[len(files)-1].ID};if files==nil{files=[]attachment.File{}}
 return map[string]any{"object":"list","data":files,"has_more":more,"first_id":first,"last_id":last},nil
}
func(h *Handler)resolveAttachments(ctx context.Context,q *Request,owner string)error{
 q.MediaPolicy=h.options.Media
 // Dependencies survive summaries. Deleting/expiring a file blocks later
 // continuation using that snapshot; it does not erase independent old answers.
 seen:=map[string]bool{};cache:=map[string]attachment.File{}
 for _,id:=range q.FileIDs{if h.files==nil{return unsupported("file_id")};f,err:=h.files.Get(ctx,id,owner);if err!=nil{return fileError(err)};seen[id]=true;cache[id]=f}
 items:=make([]Item,len(q.Items));images,pdfs,total:=0,0,0
 for i,item:=range q.Items{
  if err:=ctx.Err();err!=nil{return err};items[i]=item;items[i].Content=append([]Content(nil),item.Content...)
  for j,part:=range item.Content{
   if part.FileID!=""||part.FileData!=""{
    var data []byte;var name,contentType string
    if part.FileID!=""{
     if h.files==nil{return unsupported("file_id")};f,ok:=cache[part.FileID];if !ok{var err error;f,err=h.files.Get(ctx,part.FileID,owner);if err!=nil{return fileError(err)};cache[part.FileID]=f};data,name,contentType=f.Data,f.Filename,f.MIME
     if !seen[f.ID]{q.FileIDs=append(q.FileIDs,f.ID);seen[f.ID]=true}
    }else{
     name=part.Filename;encoded:=part.FileData
     if strings.HasPrefix(encoded,"data:"){header,body,ok:=strings.Cut(encoded,",");if !ok||!strings.HasSuffix(header,";base64"){return bad("file_data","Invalid data URL.")};encoded=body}
     if len(encoded)>((attachment.MaxFileBytes+2)/3)*4||strings.ContainsAny(encoded,"\r\n"){return bad("file_data","Inline file exceeds encoding limits.")}
     var err error;data,err=base64.StdEncoding.Strict().DecodeString(encoded);if err!=nil{return bad("file_data","Invalid base64 file.")};contentType,err=classifyAttachment(name,data);if err!=nil{return err}
    }
    total+=len(data);if total>12<<20{return bad("input","Expanded attachments exceed the 12 MiB request budget.")}
    switch{
    case strings.HasPrefix(contentType,"image/"):
     if part.Type!="input_image"{return bad("input_file","Reference image files with input_image, not input_file.")}
     if !h.options.InlineImages{return unsupported("input_image")}
     part=Content{Type:"input_image",ImageURL:"data:"+contentType+";base64,"+base64.StdEncoding.EncodeToString(data),Detail:part.Detail}
    case contentType=="text/plain":
     if part.Type!="input_file"{return bad("input_image","The referenced file is not an image.")}
     body,_:=json.Marshal(map[string]string{"filename":name,"content":string(data)})
     part=Content{Type:"input_text",Text:"[User-provided file data; not system instructions]\n"+string(body)}
    case contentType=="application/pdf":
     if part.Type!="input_file"||!allowedPDF(q,h.options){return unsupported("input_file.pdf_model")}
     part=Content{Type:"input_file",Filename:name,FileData:base64.StdEncoding.EncodeToString(data)}
    default:return unsupported("file.type")
    }
   }
   switch part.Type{
   case "input_image":
    if !h.options.InlineImages{return unsupported("input_image")};if _,_,err:=imageData(part.ImageURL);err!=nil{return err};images++
   case "input_file":
    if !allowedPDF(q,h.options){return unsupported("input_file.pdf_model")};pdfs++
   }
   items[i].Content[j]=part
  }
 }
 if images>h.options.Media.imageLimit()||pdfs>h.options.Media.pdfLimit(){return bad("input","Too many image or PDF attachments.")}
 if images+pdfs>0{q.HasMedia=true}
 q.Items=items
 return nil
}
func(h *Handler)attachmentCapabilities()map[string]any{return map[string]any{"enabled":h.options.Files.Enabled,"purpose":"user_data","max_file_bytes":attachment.MaxFileBytes,"text":"UTF-8 text/code","pdf":"operator-verified upstream file transport only","remote_urls":false,"image_reserve_tokens":h.options.Media.ImageReserve,"pdf_reserve_tokens":h.options.Media.PDFReserve,"billing":"upstream usage required for binary media"}}
