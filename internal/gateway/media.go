package gateway

import (
 "bytes"
 "encoding/base64"
 "encoding/json"
 "errors"
 "image"
 _ "image/gif"
 _ "image/jpeg"
 _ "image/png"
 "net/http"
 "path/filepath"
 "strings"
 "unicode/utf8"

 _ "golang.org/x/image/webp"
 "github.com/oai-prism/oaiprism/internal/attachment"
)

// Reserves are operator-tested upper bounds for admission only. They never
// become billing counts, nor claim tokenizer equivalence for a private model.
type MediaPolicy struct {
 ImageReserve int `yaml:"image_reserve_tokens"`
 PDFReserve int `yaml:"pdf_reserve_tokens"`
 PDFModels []string `yaml:"pdf_models"`
 MaxImages int `yaml:"max_images"`
 MaxPDFs int `yaml:"max_pdfs"`
}
func (m MediaPolicy)validate()error{
 if m.ImageReserve<0||m.ImageReserve>1000000||m.PDFReserve<0||m.PDFReserve>1000000||m.MaxImages<0||m.MaxImages>32||m.MaxPDFs<0||m.MaxPDFs>8{return errors.New("invalid media admission bounds")};return nil
}
func (m MediaPolicy)imageLimit()int{if m.MaxImages>0{return m.MaxImages};return 8}
func (m MediaPolicy)pdfLimit()int{if m.MaxPDFs>0{return m.MaxPDFs};return 2}
func allowedPDF(q *Request,o Options)bool{for _,model:=range o.Media.PDFModels{if model==q.Model{return true}};return false}
func checkedFilename(name string)error{
 if name==""||len(name)>255||!utf8.ValidString(name)||name=="."||name==".."||strings.ContainsAny(name,"/\\\x00\r\n")||filepath.Base(name)!=name{return bad("filename","Use a plain filename without directory components.")}
 for _,r:=range name{if r<32||r==127{return bad("filename","Control characters are not permitted.")}};return nil
}
func inspectImage(data []byte)(string,error){
 if len(data)==0||len(data)>6<<20{return "",bad("image","Image must be 1 byte..6 MiB.")}
 mime:=http.DetectContentType(data)
 if mime!="image/png"&&mime!="image/jpeg"&&mime!="image/gif"&&mime!="image/webp"{return "",unsupported("image.type")}
 config,_,err:=image.DecodeConfig(bytes.NewReader(data));if err!=nil{return "",bad("image","Invalid image header.")}
 if config.Width<1||config.Height<1||config.Width>16384||config.Height>16384||int64(config.Width)*int64(config.Height)>40000000{return "",bad("image","Image dimensions exceed gateway limits.")}
 return mime,nil
}
func imageData(url string)([]byte,string,error){
 header,encoded,ok:=strings.Cut(url,",");if !ok||!strings.HasPrefix(header,"data:image/")||!strings.HasSuffix(header,";base64"){return nil,"",bad("image_url","Only inline base64 images are accepted; no remote fetch is performed.")}
 if len(encoded)>8<<20||strings.ContainsAny(encoded,"\r\n"){return nil,"",bad("image_url","Invalid or oversized image encoding.")}
 data,err:=base64.StdEncoding.Strict().DecodeString(encoded);if err!=nil{return nil,"",bad("image_url","Invalid base64 image.")}
 mime,err:=inspectImage(data);if err!=nil{return nil,"",err}
 if header!="data:"+mime+";base64"{return nil,"",bad("image_url","Image bytes do not match the declared media type.")};return data,mime,nil
}
func classifyAttachment(name string,data []byte)(string,error){
 if err:=checkedFilename(name);err!=nil{return "",err}
 if len(data)==0||len(data)>attachment.MaxFileBytes{return "",bad("file","File exceeds the 8 MiB gateway limit or is empty.")}
 if strings.HasPrefix(http.DetectContentType(data),"image/"){return inspectImage(data)}
 if bytes.HasPrefix(data,[]byte("%PDF-")){
  if strings.ToLower(filepath.Ext(name))!=".pdf"||!bytes.Contains(data[max(0,len(data)-4096):],[]byte("%%EOF")){return "",bad("file","Invalid PDF envelope.")};return "application/pdf",nil
 }
 ext:=strings.ToLower(filepath.Ext(name));textTypes:=map[string]bool{".txt":true,".md":true,".json":true,".jsonl":true,".csv":true,".tsv":true,".xml":true,".html":true,".css":true,".js":true,".mjs":true,".jsx":true,".ts":true,".tsx":true,".go":true,".py":true,".rs":true,".java":true,".c":true,".h":true,".cpp":true,".sh":true,".sql":true,".yaml":true,".yml":true,".toml":true,".log":true}
 if !textTypes[ext]||!utf8.Valid(data)||bytes.IndexByte(data,0)>=0{return "",unsupported("file.type")}
 for _,b:=range data{if b<32&&b!='\t'&&b!='\n'&&b!='\r'{return "",bad("file","Unsupported control character in text file.")}}
 return "text/plain",nil
}
func parseAttachmentBlock(m map[string]json.RawMessage,role string,o Options)(Content,bool,error){
 var typ string;_ = json.Unmarshal(m["type"],&typ)
 if typ!="input_file"&&!(typ=="input_image"&&m["file_id"]!=nil){return Content{},false,nil}
 if role!="user"{return Content{},true,unsupported("content.file_role")}
 if err:=keys(m,"type file_id file_data filename detail");err!=nil{return Content{},true,err}
 part:=Content{Type:typ}
 if raw,ok:=m["file_id"];ok{
  if !o.Files.Enabled{return part,true,unsupported("file_id")}
  if err:=scalar(raw,&part.FileID,"file_id");err!=nil{return part,true,err}
  if !strings.HasPrefix(part.FileID,"file-")||len(part.FileID)>128{return part,true,bad("file_id","Invalid file identifier.")}
  if m["file_data"]!=nil||m["filename"]!=nil{return part,true,bad("file_id","Do not combine a stored file reference with inline data or filename.")}
 }else{
  if typ!="input_file"||!o.Files.Enabled{return part,true,unsupported("input_file")}
  if err:=scalar(m["filename"],&part.Filename,"filename");err!=nil{return part,true,err}
  if err:=checkedFilename(part.Filename);err!=nil{return part,true,err}
  if err:=scalar(m["file_data"],&part.FileData,"file_data");err!=nil{return part,true,err}
  if len(part.FileData)>MaxBody{return part,true,bad("file_data","Inline file is too large.")}
 }
 if raw,ok:=m["detail"];ok{if typ!="input_image"{return part,true,unsupported("detail")};if err:=scalar(raw,&part.Detail,"detail");err!=nil{return part,true,err};if part.Detail!="auto"&&part.Detail!="low"&&part.Detail!="high"{return part,true,unsupported("detail")}}
 return part,true,nil
}
// RenderedMediaTokens removes binary payloads from tokenization. Fixed admission
// reserves are explicitly required before admitting image/PDF context budgets.
func RenderedMediaTokens(q *Request)(int,error){
 input:=RenderInput(q);reserve:=0
 for i:=range input{for j,part:=range input[i].Content{switch part.Type{
 case "input_image":if q.MediaPolicy.ImageReserve<=0{return 0,unsupported("context_budget_with_images")};reserve+=q.MediaPolicy.ImageReserve;input[i].Content[j]=Content{Type:"input_text",Text:"[image admission reserve]"}
 case "input_file":if q.MediaPolicy.PDFReserve<=0{return 0,unsupported("context_budget_with_pdf")};reserve+=q.MediaPolicy.PDFReserve;input[i].Content[j]=Content{Type:"input_text",Text:"[PDF admission reserve]"}
 }}}
 raw,err:=json.Marshal(input);if err!=nil{return 0,err};n,err:=CountTokens(string(raw));return n+reserve,err
}
