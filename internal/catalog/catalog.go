// Package catalog reads operator-selected model metadata, never user prompts or
// model self-identification. A declaration is not proof of successful inference.
package catalog

import (
 "bytes"
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net"
 "net/http"
 "net/url"
 "os"
 "sort"
 "strings"
 "sync"
 "time"
)

const MaxSnapshotBytes=2<<20

type Source struct {
 Name string `yaml:"name"`
 AccountID string `yaml:"account_id"`
 Path string `yaml:"path"`
 URL string `yaml:"url"`
 BearerEnv string `yaml:"bearer_env"`
}
type Options struct {
 Sources []Source `yaml:"sources"`
 Publish []string `yaml:"publish"`
 RefreshInterval time.Duration `yaml:"refresh_interval"`
 MaxAge time.Duration `yaml:"max_age"`
}
type Model struct {
 ID string `json:"id"`
 UpstreamID string `json:"upstream_id"`
 DisplayName string `json:"display_name"`
 ReasoningEfforts []string `json:"reasoning_efforts"`
 DefaultEffort string `json:"default_effort"`
 ContextWindow int `json:"context_window"`
 InputModalities []string `json:"input_modalities"`
 // These are upstream declarations, not gateway emulation guarantees.
 Capabilities map[string]bool `json:"capabilities"`
}
type Snapshot struct {
 ObservedAt time.Time `json:"observed_at"`
 Models []Model `json:"models"`
}
type Record struct {
 Model
 AccountID string `json:"-"`
 Source string `json:"source"`
 ObservedAt time.Time `json:"observed_at"`
 Stale bool `json:"stale"`
 Evidence string `json:"evidence"`
}
type sourceState struct {snapshot Snapshot;success time.Time;lastError bool}
type Registry struct {
 options Options
 mu sync.RWMutex
 refresh sync.Mutex
 states map[string]sourceState
 client *http.Client
 cancel context.CancelFunc
 wg sync.WaitGroup
}
func validID(s string,max int)bool{
 if s==""||len(s)>max{return false}
 for _,c:=range s{if c<33||c>126||strings.ContainsRune("\\\"<>?#",c){return false}}
 return true
}
func(o Options)Validate()error{
 if len(o.Sources)>32||len(o.Publish)>512{return errors.New("catalog exceeds source/model limit")}
 if o.RefreshInterval<0||o.MaxAge<0{return errors.New("catalog durations cannot be negative")}
 if o.RefreshInterval>0&&o.RefreshInterval<30*time.Second{return errors.New("catalog refresh_interval must be at least 30s")}
 if o.MaxAge>0&&o.MaxAge<time.Minute{return errors.New("catalog max_age must be at least 1m")}
 names:=map[string]bool{}
 for _,s:=range o.Sources{
  if !validID(s.Name,64)||!validID(s.AccountID,128)||names[s.Name]{return errors.New("catalog sources need unique names and an operator-owned account_id")};names[s.Name]=true
  if (s.Path=="")== (s.URL==""){return errors.New("catalog source requires exactly one path or URL")}
  if s.URL!=""{
   u,e:=url.Parse(s.URL);if e!=nil||u.Hostname()==""||u.User!=nil||u.Fragment!=""{return errors.New("invalid catalog URL")}
   ip:=net.ParseIP(u.Hostname())
   if u.Scheme!="https"&&!(u.Scheme=="http"&&ip!=nil&&ip.IsLoopback()){return errors.New("catalog URL requires HTTPS or a literal loopback HTTP address")}
  }
 }
 for _,id:=range o.Publish{if !validID(id,256){return errors.New("invalid published model ID")}}
 return nil
}
func(o Options)interval()time.Duration{if o.RefreshInterval>0{return o.RefreshInterval};return 5*time.Minute}
func(o Options)age()time.Duration{if o.MaxAge>0{return o.MaxAge};return time.Hour}
func New(o Options)(*Registry,error){
 if err:=o.Validate();err!=nil{return nil,err}
 // Own copies: caller changes cannot race with refresh and routing.
 o.Sources=append([]Source(nil),o.Sources...);o.Publish=append([]string(nil),o.Publish...)
 r:=&Registry{options:o,states:map[string]sourceState{},client:&http.Client{Timeout:10*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return errors.New("catalog redirects are not followed")}}}
 ctx,cancel:=context.WithCancel(context.Background());r.cancel=cancel
 // One bounded initial read allows file-backed catalogs to work immediately.
 r.Refresh(ctx)
 if len(o.Sources)>0{r.wg.Add(1);go func(){defer r.wg.Done();ticker:=time.NewTicker(o.interval());defer ticker.Stop();for{select{case<-ctx.Done():return;case<-ticker.C:r.Refresh(ctx)}}}()}
 return r,nil
}
func(r *Registry)Close(){r.cancel();r.wg.Wait();r.client.CloseIdleConnections()}
func(r *Registry)Refresh(ctx context.Context){
 r.refresh.Lock();defer r.refresh.Unlock()
 for _,source:=range r.options.Sources{
  if ctx.Err()!=nil{return}
  snapshot,err:=r.read(ctx,source)
  r.mu.Lock()
  state:=r.states[source.Name]
  if err==nil{state.snapshot=snapshot;state.success=time.Now();state.lastError=false}else{state.lastError=true}
  r.states[source.Name]=state;r.mu.Unlock()
 }
}
func(r *Registry)read(ctx context.Context,s Source)(Snapshot,error){
 var reader io.ReadCloser
 if s.Path!=""{
  f,err:=os.Open(s.Path);if err!=nil{return Snapshot{},err};info,err:=f.Stat();if err!=nil||!info.Mode().IsRegular()||info.Size()>MaxSnapshotBytes{f.Close();return Snapshot{},errors.New("invalid catalog file")};reader=f
 }else{
  req,err:=http.NewRequestWithContext(ctx,http.MethodGet,s.URL,nil);if err!=nil{return Snapshot{},err}
  req.Header.Set("Accept","application/json")
  if s.BearerEnv!=""{key:=os.Getenv(s.BearerEnv);if key==""{return Snapshot{},errors.New("missing catalog credential")};req.Header.Set("Authorization","Bearer "+key)}
  resp,err:=r.client.Do(req);if err!=nil{return Snapshot{},err};reader=resp.Body
  if resp.StatusCode!=200{reader.Close();return Snapshot{},fmt.Errorf("catalog status %d",resp.StatusCode)}
 }
 defer reader.Close();raw,err:=io.ReadAll(io.LimitReader(reader,MaxSnapshotBytes+1));if err!=nil||len(raw)>MaxSnapshotBytes{return Snapshot{},errors.New("catalog read/size error")}
 return Decode(raw,time.Now(),r.options.age())
}
func Decode(raw []byte,now time.Time,maxAge time.Duration)(Snapshot,error){
 if len(raw)>MaxSnapshotBytes{return Snapshot{},errors.New("oversized catalog")}
 var snap Snapshot;d:=json.NewDecoder(bytes.NewReader(raw));d.DisallowUnknownFields()
 if err:=d.Decode(&snap);err!=nil{return Snapshot{},err};if d.Decode(new(any))!=io.EOF{return Snapshot{},errors.New("trailing catalog data")}
 if snap.ObservedAt.IsZero()||snap.ObservedAt.After(now.Add(5*time.Minute))||now.Sub(snap.ObservedAt)>maxAge{return Snapshot{},errors.New("catalog observation timestamp missing or stale")}
 if len(snap.Models)>512{return Snapshot{},errors.New("too many catalog models")}
 seen:=map[string]bool{}
 for i:=range snap.Models{
  m:=&snap.Models[i];if !validID(m.ID,256)||seen[m.ID]{return Snapshot{},errors.New("duplicate/invalid catalog model")};seen[m.ID]=true
  if m.UpstreamID==""{m.UpstreamID=m.ID};if !validID(m.UpstreamID,256)||len(m.DisplayName)>256{return Snapshot{},errors.New("invalid upstream model")}
  if m.ContextWindow<0||m.ContextWindow>2000000||len(m.ReasoningEfforts)>32||len(m.InputModalities)>8||len(m.Capabilities)>32{return Snapshot{},errors.New("catalog field exceeds limit")}
  efforts:=map[string]bool{};for _,v:=range m.ReasoningEfforts{if !validID(v,32)||efforts[v]{return Snapshot{},errors.New("invalid reasoning efforts")};efforts[v]=true}
  if m.DefaultEffort!=""&&!efforts[m.DefaultEffort]{return Snapshot{},errors.New("default effort is not declared")}
  for _,v:=range m.InputModalities{if v!="text"&&v!="image"&&v!="audio"{return Snapshot{},errors.New("invalid input modality")}}
  for key:=range m.Capabilities{if !validID(key,64){return Snapshot{},errors.New("invalid capability name")}}
 }
 return snap,nil
}
func(r *Registry)Records()[]Record{
 r.mu.RLock();defer r.mu.RUnlock();out:=[]Record{};now:=time.Now()
 for _,source:=range r.options.Sources{state:=r.states[source.Name];for _,model:=range state.snapshot.Models{
  copy:=model;copy.ReasoningEfforts=append([]string(nil),model.ReasoningEfforts...);copy.InputModalities=append([]string(nil),model.InputModalities...);copy.Capabilities=map[string]bool{};for k,v:=range model.Capabilities{copy.Capabilities[k]=v}
  out=append(out,Record{Model:copy,AccountID:source.AccountID,Source:source.Name,ObservedAt:state.snapshot.ObservedAt,Stale:now.Sub(state.snapshot.ObservedAt)>r.options.age()||now.Sub(state.success)>r.options.age(),Evidence:"declared"})
 }}
 sort.Slice(out,func(i,j int)bool{if out[i].ID==out[j].ID{return out[i].AccountID<out[j].AccountID};return out[i].ID<out[j].ID});return out
}
func(r *Registry)Published(id string)bool{for _,name:=range r.options.Publish{if name==id{return true}};return false}
// Select only returns matching, non-stale account declarations. Conflicting
// upstream IDs fail closed. An HTTP success never upgrades evidence to 'native'.
func(r *Registry)Select(id,effort string)(Model,[]string,error){
 var chosen Model;accounts:=[]string{};known:=false
 for _,record:=range r.Records(){if record.ID!=id{continue};known=true;if record.Stale{continue};effective:=effort;if effective==""{effective=record.DefaultEffort}
  if effective!=""{ok:=false;for _,v:=range record.ReasoningEfforts{ok=ok||v==effective};if !ok{continue}}
  if chosen.ID!=""&&(chosen.UpstreamID!=record.UpstreamID||chosen.DefaultEffort!=record.DefaultEffort&&effort==""){return Model{},nil,errors.New("conflicting model declarations")}
  if chosen.ID==""{chosen=record.Model}else if record.ContextWindow>0&&(chosen.ContextWindow==0||record.ContextWindow<chosen.ContextWindow){chosen.ContextWindow=record.ContextWindow}
  if !contains(accounts,record.AccountID){accounts=append(accounts,record.AccountID)}
 }
 if len(accounts)==0{if known{return Model{},nil,errors.New("no fresh account declaration supports the requested effort")};return Model{},nil,errors.New("model declaration unavailable")}
 return chosen,accounts,nil
}
func contains(v []string,s string)bool{for _,x:=range v{if x==s{return true}};return false}
