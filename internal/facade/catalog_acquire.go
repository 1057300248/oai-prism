package facade

import (
 "bytes"
 "context"
 "crypto/sha256"
 "sort"
 "time"

 "github.com/oai-prism/oaiprism/internal/account"
)

// Catalog account IDs come only from operator-selected metadata sources. User
// request headers, tool names and client_metadata never populate this list.
func(r *Runner)acquireCatalog(ctx context.Context,req *RunRequest)(*account.Lease,error){
 if err:=ctx.Err();err!=nil{return nil,err}
 ids:=append([]string(nil),req.AllowedAccounts...)
 if req.AccountID!=""{
  match:=false;for _,id:=range ids{match=match||id==req.AccountID};if !match{return nil,account.ErrNoAccount};ids=[]string{req.AccountID}
 }else{
  key:=req.StickyKey
  if key==""{key=time.Now().UTC().Format(time.RFC3339Nano)}
  sort.Slice(ids,func(i,j int)bool{a:=sha256.Sum256([]byte(key+"\x00"+ids[i]));b:=sha256.Sum256([]byte(key+"\x00"+ids[j]));return bytes.Compare(a[:],b[:])<0})
 }
 for _,id:=range ids{
  if err:=ctx.Err();err!=nil{return nil,err}
  if a:=r.pool.Get(id);a!=nil&&a.Acquire(time.Now()){r.app.AccountPick.Inc("catalog");return &account.Lease{Account:a},nil}
 }
 return nil,account.ErrNoAccount // no fallback to an account without the capability
}
