package main
import (
 "context"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
 "time"
)
type fakeDB struct{call func(string,any,any)error}
func (f fakeDB) Call(_ context.Context,name string,data,out any)error{if f.call!=nil{return f.call(name,data,out)};return nil}
type fakeRules struct{}
func(fakeRules)Check(_ context.Context,o *Order)error{o.Validation=Totals{Subtotal:6000,Total:o.Amount,Units:3,CheckedLines:true};return nil}
func testServer(db Database)*Server{return &Server{DB:db,Rules:fakeRules{},Config:Config{AdminToken:"admin-test",DemoSecret:"demo-secret",StripeSecret:"stripe-secret",DestinationToken:"dest-secret"}}}
func perform(handler http.Handler,method,path,body string,headers map[string]string)*httptest.ResponseRecorder{
 req:=httptest.NewRequest(method,path,strings.NewReader(body));for k,v:=range headers{req.Header.Set(k,v)};res:=httptest.NewRecorder();handler.ServeHTTP(res,req);return res
}
func TestAdminAuthenticationAndCSRF(t *testing.T){
 handler:=testServer(fakeDB{}).Handler()
 if perform(handler,"GET","/api/overview","",nil).Code!=401{t.Fatal("unauthenticated read allowed")}
 login:=perform(handler,"POST","/api/login",`{"token":"admin-test"}`,nil)
 if login.Code!=200||len(login.Result().Cookies())!=1{t.Fatal("login failed")}
 cookie:=login.Result().Cookies()[0];if !cookie.HttpOnly||cookie.SameSite!=http.SameSiteStrictMode{t.Fatal("unsafe session cookie")}
 res:=perform(handler,"POST","/api/demo",`{"action":"new"}`,map[string]string{"Cookie":cookie.String()})
 if res.Code!=403{t.Fatal("cookie write without CSRF header allowed")}
 res=perform(handler,"POST","/api/demo",`{"action":"new"}`,map[string]string{"Authorization":"Bearer admin-test","Origin":"https://evil.example"})
 if res.Code!=403{t.Fatal("cross-origin write allowed")}
 bad:=perform(handler,"POST","/api/login",`{"token":"wrong"}`,nil);if bad.Code!=401{t.Fatal("bad token accepted")}
}
func TestSignedWebhookQueuesOnlyAfterAuthentication(t *testing.T){
 calls:=0;db:=fakeDB{call:func(name string,input,out any)error{calls++;if name!="ingest_event"{t.Fatal(name)};args:=input.(map[string]any);if args["p_source"]!="demo"{t.Fatal("wrong source")};result:=out.(*IngestResult);*result=IngestResult{OrderID:"order",Status:"accepted"};return nil}}
 handler:=testServer(db).Handler();body,_:=json.Marshal(sampleEvent());now:=time.Now().Unix();signature:=fmt.Sprintf("t=%d,v1=%s",now,signPayload("demo-secret",now,body))
 good:=perform(handler,"POST","/webhooks/demo",string(body),map[string]string{"Relay-Signature":signature});if good.Code!=202||calls!=1{t.Fatalf("webhook failed: %d %s",good.Code,good.Body.String())}
 bad:=perform(handler,"POST","/webhooks/demo",string(body),nil);if bad.Code!=401||calls!=1{t.Fatal("unsigned event reached database")}
 large:=perform(handler,"POST","/webhooks/demo",strings.Repeat("x",65537),nil);if large.Code!=413{t.Fatal("oversized webhook accepted")}
}
func TestIgnoredStripeDoesNotCreateJobs(t *testing.T){
 handler:=testServer(fakeDB{call:func(string,any,any)error{t.Fatal("ignored event reached database");return nil}}).Handler()
 body:=[]byte(`{"id":"evt_other","type":"customer.created","livemode":false}`);now:=time.Now().Unix();header:=fmt.Sprintf("t=%d,v1=%s",now,signPayload("stripe-secret",now,body))
 if res:=perform(handler,"POST","/webhooks/stripe",string(body),map[string]string{"Stripe-Signature":header});res.Code!=200{t.Fatal(res.Code)}
}
func TestDestinationRequiresCredentialAndDeliveryKey(t *testing.T){
 handler:=testServer(fakeDB{}).DestinationHandler()
 if perform(handler,"POST","/orders",`{}`,nil).Code!=401{t.Fatal("unauthenticated destination allowed")}
 if perform(handler,"POST","/orders",`{}`,map[string]string{"Authorization":"Bearer dest-secret"}).Code!=400{t.Fatal("missing idempotency key allowed")}
}
type roundTripFunc func(*http.Request)(*http.Response,error)
func(f roundTripFunc)RoundTrip(r *http.Request)(*http.Response,error){return f(r)}
func TestWorkerUsesStableIdempotencyKeyAndRecordsRetry(t *testing.T){
 id:="11111111-1111-1111-1111-111111111111";token:="22222222-2222-2222-2222-222222222222";finished:=false
 db:=fakeDB{call:func(name string,input,out any)error{
  if name=="claim_delivery"{o:=out.(**Job);*o=&Job{ID:id,Token:token,Attempt:1,Payload:json.RawMessage(`{"version":1}`)};return nil}
  if name=="finish_delivery"{args:=input.(map[string]any);if args["p_id"]!=id||args["p_token"]!=token||args["p_http_status"]!=503||args["p_retryable"]!=true{t.Fatalf("bad completion: %+v",args)};*(out.(*bool))=true;finished=true;return nil};t.Fatal(name);return nil
 }}
 client:=&http.Client{Transport:roundTripFunc(func(r *http.Request)(*http.Response,error){
  if r.Header.Get("Idempotency-Key")!=id||r.Header.Get("Authorization")!="Bearer receiver"{t.Fatal("missing stable delivery headers")}
  return &http.Response{StatusCode:503,Header:http.Header{"Retry-After":[]string{"2"}},Body:io.NopCloser(strings.NewReader(`{"error":"outage"}`))},nil
 })}
 worker:=Worker{DB:db,Client:client,URL:"http://destination/orders",Token:"receiver"};worked,err:=worker.Step(t.Context());if err!=nil||!worked||!finished{t.Fatal(worked,err)}
}
func TestRetryClassificationAndBoundedBackoff(t *testing.T){
 for _,status:=range []int{0,408,425,429,500,503}{if !retryable(status){t.Fatal("should retry",status)}}
 for _,status:=range []int{200,301,400,401,403,404,409,422}{if retryable(status){t.Fatal("should not retry",status)}}
 for attempt:=1;attempt<12;attempt++{d:=retryDelay(attempt,"99999",time.Now());if d!=300{t.Fatal("Retry-After cap failed",d)}}
 if d:=retryDelay(1,"",time.Now());d<2||d>=3{t.Fatal("initial backoff outside range",d)}
}
func TestDatabaseErrorDoesNotLeakRemoteBody(t *testing.T){
 client:=&http.Client{Transport:roundTripFunc(func(r *http.Request)(*http.Response,error){return &http.Response{StatusCode:500,Body:io.NopCloser(strings.NewReader(`{"message":"private-secret"}`)),Header:http.Header{}},nil})}
 store:=RESTStore{URL:"http://rest",Client:client};err:=store.Call(t.Context(),"ping",map[string]any{},nil)
 if err==nil||strings.Contains(err.Error(),"private-secret"){t.Fatal("remote details leaked")}
}
