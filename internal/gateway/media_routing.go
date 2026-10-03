package gateway

// Media support is an account-specific requirement. A text-only account must
// never receive a vision request merely because it shares the public model ID.
func(h *Handler)filterMediaAccounts(q *Request,image,pdf bool)error{
 if q.ResolvedModel==""{return nil} // Static models remain explicit operator configuration.
 permitted:=map[string]bool{};for _,id:=range q.AllowedAccounts{permitted[id]=true}
 status:=map[string]bool{}
 for _,r:=range h.catalog.Records(){
  if r.ID!=q.Model||r.UpstreamID!=q.ResolvedModel||r.Stale||!permitted[r.AccountID]{continue}
  supports:=true
  if image{found:=false;for _,mode:=range r.InputModalities{found=found||mode=="image"};supports=supports&&found}
  if pdf{supports=supports&&r.Capabilities["pdf_input"]}
  if previous,exists:=status[r.AccountID];exists{supports=supports&&previous}
  status[r.AccountID]=supports
 }
 allowed:=[]string{};for _,id:=range q.AllowedAccounts{if status[id]{allowed=append(allowed,id)}}
 if len(allowed)==0{return &APIError{Status:503,Code:"model_media_unavailable",Param:"model",Message:"No fresh account declaration supports the requested image/PDF input."}}
 q.AllowedAccounts=allowed;return nil
}
