package gateway

import (
 "context"
 "encoding/json"
 "errors"
 "io"
 "net/http"
 "net/http/httptest"
 "net/url"
 "strings"
 "testing"
)

func TestInputItemsPaginationExcludesCurrentOutput(t *testing.T) {
 h:=harness(t,okEngine,func(o *Options){o.ResponseStore=true;o.TenantHeader="X-Test-Tenant";o.TrustedPeers=[]string{"127.0.0.1/32"}})
 body:=`{"model":"test-model","input":[{"role":"user","content":"first"},{"role":"assistant","content":"earlier answer"},{"role":"user","content":"second"}],"store":true}`
 w:=call(h,"POST","/v1/responses",body,"key-a","tenant");if w.Code!=200{t.Fatal(w.Body.String())};id:=readMap(t,w)["id"].(string)
 first:=call(h,"GET","/v1/responses/"+id+"/input_items?limit=1","","key-a","tenant");page:=readMap(t,first)
 if page["has_more"]!=true{t.Fatal("missing next page",page)}
 data:=page["data"].([]any);item:=data[0].(map[string]any)
 if item["content"].([]any)[0].(map[string]any)["text"]!="second"{t.Fatal("current output leaked into input list",item)}
 cursor:=page["last_id"].(string)
 next:=call(h,"GET","/v1/responses/"+id+"/input_items?limit=100&after="+url.QueryEscape(cursor),"","key-a","tenant");second:=readMap(t,next)
 if len(second["data"].([]any))!=2||second["has_more"]!=false{t.Fatal("wrong input cursor page",second)}
 asc:=call(h,"GET","/v1/responses/"+id+"/input_items?order=asc","","key-a","tenant");a:=readMap(t,asc)["data"].([]any)
 if a[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]!="first"{t.Fatal("ascending order not honored")}
 for _,query:=range []string{"limit=0","limit=101","order=wrong","after=other-response","limit=1&limit=2","include=private"}{if call(h,"GET","/v1/responses/"+id+"/input_items?"+query,"","key-a","tenant").Code!=400{t.Error("invalid pagination accepted",query)}}
}
func TestTransportBodyLimitAndCompression(t *testing.T){
 h:=harness(t,okEngine,nil)
 r:=httptest.NewRequest("POST","/v1/responses",strings.NewReader(strings.Repeat("x",MaxBody+1)));r.ContentLength=-1;r.Header.Set("Authorization","Bearer key-a")
 w:=httptest.NewRecorder();h.ServeHTTP(w,r);if w.Code!=413{t.Fatal("chunked limit bypass",w.Code)}
 r=httptest.NewRequest("POST","/v1/responses",strings.NewReader("compressed"));r.Header.Set("Authorization","Bearer key-a");r.Header.Set("Content-Encoding","gzip")
 w=httptest.NewRecorder();h.ServeHTTP(w,r);if w.Code!=415{t.Fatal("unsupported compression accepted")}
 r=httptest.NewRequest("POST","/v1/responses",strings.NewReader(`{"model":"test-model","input":"x"}`));r.Header.Add("Authorization","Bearer key-a");r.Header.Add("Authorization","Bearer key-b")
 w=httptest.NewRecorder();h.ServeHTTP(w,r);if w.Code!=401{t.Fatal("ambiguous authentication accepted")}
}

type shortWriter struct{header http.Header}
func(w *shortWriter)Header()http.Header{if w.header==nil{w.header=make(http.Header)};return w.header}
func(w *shortWriter)WriteHeader(int){}
func(w *shortWriter)Write(b []byte)(int,error){return len(b)-1,nil}
func(w *shortWriter)Flush(){}
func TestSSEShortWriteFailsWithoutCompletion(t *testing.T){
 writer:=&shortWriter{};s:=newStream(writer,&Request{Model:"test-model"},true,"resp_test",1);defer s.close()
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 if err:=s.open(ctx,cancel);!errors.Is(err,io.ErrShortWrite){t.Fatalf("short write ignored: %v",err)}
 if s.terminal{t.Fatal("failed write marked completed")}
}
func TestPersistenceFailureDoesNotEmitCompleted(t *testing.T){
 rec:=httptest.NewRecorder();q:=&Request{Model:"test-model",Metadata:map[string]string{},Format:"text"}
 s:=newStream(rec,q,true,"resp_test",1);defer s.close();ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 if err:=s.open(ctx,cancel);err!=nil{t.Fatal(err)}
 s.beforeTerminal=func(map[string]any)error{return errors.New("persistent store unavailable")}
 if _,err:=s.finish(&Result{Text:"answer"},nil);err==nil{t.Fatal("failed persistence became success")}else{_ = s.fail(err)}
 if strings.Contains(rec.Body.String(),"response.completed")||!strings.Contains(rec.Body.String(),"response.failed"){t.Fatal(rec.Body.String())}
}
func TestStructuredSchemaNeverFetchesExternalMetaSchema(t *testing.T){
 _,err:=compileSchema(json.RawMessage(`{"$schema":"http://127.0.0.1:1/private","type":"object"}`))
 if err==nil{t.Fatal("unavailable external metaschema accepted")}
}
