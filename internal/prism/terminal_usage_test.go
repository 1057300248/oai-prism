package prism

import "testing"

func TestTypedTerminalEnvelopePreservesPayloadUsage(t *testing.T){
 c:=&Client{schema:testSchema()}
 body:=[]byte(`{"status":"completed","request_id":"typed-path","response":{"status":"success","payload":{"id":"resp_upstream","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":100,"output_tokens":4,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":40}}}}}`)
 st,err:=c.ParseStatusPayload(body,"req","");if err!=nil{t.Fatal(err)}
 if !st.Done||st.Fail||st.Usage==nil||st.Usage.Invalid||st.Usage.InputTokens!=100||st.Usage.CacheWriteTokens==nil||*st.Usage.CacheWriteTokens!=40{t.Fatalf("typed terminal usage lost: %+v",st)}
}
