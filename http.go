package main
import (
 "context"
 "crypto/hmac"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "io/fs"
 "log/slog"
 "net/http"
 "regexp"
 "strconv"
 "strings"
 "time"
)
type Config struct {AdminToken,DemoSecret,StripeSecret,DestinationToken string;SecureCookies bool}
type Server struct {DB Database;Rules Reconciler;Config Config;Assets fs.FS}
var uuidPattern=regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
func respond(w http.ResponseWriter,status int,value any) {
 w.Header().Set("Content-Type","application/json");w.Header().Set("Cache-Control","no-store");w.WriteHeader(status)
 if err:=json.NewEncoder(w).Encode(value);err!=nil{slog.Debug("response closed","error",err)}
}
func fail(w http.ResponseWriter,err error) {
 var db *DBError
 if errors.As(err,&db){respond(w,db.Status,map[string]any{"error":db.Message});return}
 slog.Error("request failed","error",err)
 respond(w,503,map[string]any{"error":"A required service is unavailable. Check service health and container logs."})
}
func readBody(w http.ResponseWriter,r *http.Request)([]byte,error) {
 r.Body=http.MaxBytesReader(w,r.Body,65536)
 body,err:=io.ReadAll(r.Body)
 if err!=nil {return nil,&DBError{413,"Request body exceeds 64 KiB or could not be read"}}
 return body,nil
}
func decodeBody(w http.ResponseWriter,r *http.Request,out any) error {
 body,err:=readBody(w,r);if err!=nil{return err}
 if json.Unmarshal(body,out)!=nil{return &DBError{400,"Invalid JSON request"}}
 return nil
}
func (s *Server) authorized(r *http.Request)bool {
 auth:=r.Header.Get("Authorization")
 if strings.HasPrefix(auth,"Bearer ")&&hmac.Equal([]byte(strings.TrimPrefix(auth,"Bearer ")),[]byte(s.Config.AdminToken)){return true}
 cookie,err:=r.Cookie("relaydesk_session");if err!=nil{return false}
 parts:=strings.Split(cookie.Value,".");if len(parts)!=2{return false}
 timestamp,err:=strconv.ParseInt(parts[0],10,64);now:=time.Now().Unix()
 if err!=nil||timestamp<now-43200||timestamp>now+30{return false}
 return hmac.Equal([]byte(parts[1]),[]byte(signPayload(s.Config.AdminToken,timestamp,[]byte("admin-session"))))
}
func sameOrigin(r *http.Request)bool {
 origin:=r.Header.Get("Origin");if origin==""{return true}
 return origin=="http://"+r.Host||origin=="https://"+r.Host
}
func (s *Server) admin(fn http.HandlerFunc)http.HandlerFunc {
 return func(w http.ResponseWriter,r *http.Request){
  if !sameOrigin(r){respond(w,403,map[string]string{"error":"Cross-origin requests are not allowed"});return}
  if !s.authorized(r){respond(w,401,map[string]string{"error":"Sign in to the local workspace"});return}
  if r.Method!="GET"&&r.Header.Get("Authorization")==""&&r.Header.Get("X-Relaydesk-Request")!="1"{respond(w,403,map[string]string{"error":"Missing request protection header"});return}
  fn(w,r)
 }
}
func (s *Server) Handler()http.Handler {
 mux:=http.NewServeMux()
 mux.HandleFunc("GET /healthz",func(w http.ResponseWriter,r *http.Request){respond(w,200,map[string]bool{"ok":true})})
 mux.HandleFunc("GET /readyz",func(w http.ResponseWriter,r *http.Request){ctx,cancel:=context.WithTimeout(r.Context(),2*time.Second);defer cancel();if err:=s.DB.Call(ctx,"ping",map[string]any{},nil);err!=nil{fail(w,err);return};respond(w,200,map[string]bool{"ready":true})})
 mux.HandleFunc("POST /api/login",s.login)
 mux.HandleFunc("POST /api/logout",s.admin(func(w http.ResponseWriter,r *http.Request){http.SetCookie(w,&http.Cookie{Name:"relaydesk_session",Value:"",Path:"/",MaxAge:-1,HttpOnly:true,SameSite:http.SameSiteStrictMode,Secure:s.Config.SecureCookies});respond(w,200,map[string]bool{"ok":true})}))
 mux.HandleFunc("GET /api/overview",s.admin(func(w http.ResponseWriter,r *http.Request){var result map[string]any;if err:=s.DB.Call(r.Context(),"dashboard",map[string]any{},&result);err!=nil{fail(w,err);return};result["stripe_enabled"]=s.Config.StripeSecret!="";respond(w,200,result)}))
 mux.HandleFunc("GET /api/orders",s.admin(func(w http.ResponseWriter,r *http.Request){offset,_:=strconv.Atoi(r.URL.Query().Get("offset"));var result any;err:=s.DB.Call(r.Context(),"list_orders",map[string]any{"p_search":r.URL.Query().Get("search"),"p_status":r.URL.Query().Get("status"),"p_offset":offset},&result);if err!=nil{fail(w,err);return};respond(w,200,result)}))
 mux.HandleFunc("GET /api/orders/{id}",s.admin(func(w http.ResponseWriter,r *http.Request){id:=r.PathValue("id");if !uuidPattern.MatchString(id){respond(w,400,map[string]string{"error":"Invalid order ID"});return};var result any;err:=s.DB.Call(r.Context(),"order_details",map[string]any{"p_id":id},&result);if err!=nil{fail(w,err);return};respond(w,200,result)}))
 mux.HandleFunc("GET /api/deliveries",s.admin(s.listRPC("delivery_log")))
 mux.HandleFunc("GET /api/activity",s.admin(s.listRPC("activity")))
 mux.HandleFunc("POST /api/deliveries/{id}/retry",s.admin(func(w http.ResponseWriter,r *http.Request){id:=r.PathValue("id");if !uuidPattern.MatchString(id){respond(w,400,map[string]string{"error":"Invalid delivery ID"});return};var result any;if err:=s.DB.Call(r.Context(),"retry_delivery",map[string]any{"p_id":id},&result);err!=nil{fail(w,err);return};respond(w,200,result)}))
 mux.HandleFunc("POST /api/simulator",s.admin(func(w http.ResponseWriter,r *http.Request){var data struct{Mode string `json:"mode"`};if err:=decodeBody(w,r,&data);err!=nil{fail(w,err);return};var result any;if err:=s.DB.Call(r.Context(),"set_destination_mode",map[string]any{"p_mode":data.Mode},&result);err!=nil{fail(w,err);return};respond(w,200,result)}))
 mux.HandleFunc("POST /api/demo",s.admin(s.demo))
 mux.HandleFunc("POST /webhooks/demo",s.demoWebhook)
 mux.HandleFunc("POST /webhooks/stripe",s.stripeWebhook)
 if s.Assets!=nil{mux.Handle("GET /",http.FileServer(http.FS(s.Assets)))}
 return securityHeaders(mux)
}
func securityHeaders(next http.Handler)http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("Referrer-Policy","no-referrer")
  w.Header().Set("Content-Security-Policy","default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'self'")
  next.ServeHTTP(w,r)
 })
}
func (s *Server) listRPC(name string)http.HandlerFunc{return func(w http.ResponseWriter,r *http.Request){var result any;if err:=s.DB.Call(r.Context(),name,map[string]any{},&result);err!=nil{fail(w,err);return};respond(w,200,result)}}
func (s *Server) login(w http.ResponseWriter,r *http.Request) {
 if !sameOrigin(r){respond(w,403,map[string]string{"error":"Cross-origin login rejected"});return}
 var data struct{Token string `json:"token"`};if err:=decodeBody(w,r,&data);err!=nil{fail(w,err);return}
 if !hmac.Equal([]byte(data.Token),[]byte(s.Config.AdminToken)){respond(w,401,map[string]string{"error":"Incorrect workspace token"});return}
 timestamp:=time.Now().Unix();value:=fmt.Sprintf("%d.%s",timestamp,signPayload(s.Config.AdminToken,timestamp,[]byte("admin-session")))
 http.SetCookie(w,&http.Cookie{Name:"relaydesk_session",Value:value,Path:"/",MaxAge:43200,HttpOnly:true,SameSite:http.SameSiteStrictMode,Secure:s.Config.SecureCookies})
 respond(w,200,map[string]bool{"ok":true})
}
func (s *Server) receiveDemo(ctx context.Context,body []byte,signature string)(IngestResult,error) {
 if err:=verifySignature(s.Config.DemoSecret,signature,body,time.Now());err!=nil{return IngestResult{},&DBError{401,err.Error()}}
 event,err:=parseDemo(body);if err!=nil{return IngestResult{},&DBError{422,err.Error()}}
 return ingest(ctx,s.DB,s.Rules,"demo",event.ID,body,&event.Order,json.RawMessage(body))
}
func (s *Server) demoWebhook(w http.ResponseWriter,r *http.Request) {
 body,err:=readBody(w,r);if err!=nil{fail(w,err);return}
 result,err:=s.receiveDemo(r.Context(),body,r.Header.Get("Relay-Signature"));if err!=nil{fail(w,err);return}
 respond(w,202,result)
}
func (s *Server) stripeWebhook(w http.ResponseWriter,r *http.Request) {
 if s.Config.StripeSecret==""{respond(w,503,map[string]string{"error":"Stripe test webhook is not configured"});return}
 body,err:=readBody(w,r);if err!=nil{fail(w,err);return}
 if err:=verifySignature(s.Config.StripeSecret,r.Header.Get("Stripe-Signature"),body,time.Now());err!=nil{respond(w,401,map[string]string{"error":err.Error()});return}
 id,order,err:=parseStripe(body);if err!=nil{respond(w,422,map[string]string{"error":err.Error()});return}
 if order==nil{respond(w,200,map[string]string{"status":"ignored","event_id":id});return}
 result,err:=ingest(r.Context(),s.DB,s.Rules,"stripe",id,body,order,nil);if err!=nil{fail(w,err);return}
 respond(w,202,result)
}
func (s *Server) demo(w http.ResponseWriter,r *http.Request) {
 var data struct{Action string `json:"action"`};if err:=decodeBody(w,r,&data);err!=nil{fail(w,err);return}
 var body []byte
 if data.Action=="new" {body,_=json.Marshal(sampleEvent())} else if data.Action=="replay"||data.Action=="update" {
  var previous json.RawMessage
  if err:=s.DB.Call(r.Context(),"last_demo",map[string]any{},&previous);err!=nil{fail(w,err);return}
  if len(previous)==0||string(previous)=="null"{respond(w,422,map[string]string{"error":"Send a sample order first"});return}
  body=previous
  if data.Action=="update" {
   var event DemoEvent;if err:=json.Unmarshal(previous,&event);err!=nil{fail(w,err);return}
   event.ID="evt_demo_"+newID();event.Order.Version++;event.Order.Items[0].Quantity++;event.Order.Amount+=event.Order.Items[0].UnitAmount;event.Order.Validation=Totals{}
   body,_=json.Marshal(event)
  }
 } else {respond(w,422,map[string]string{"error":"Unknown demo action"});return}
 now:=time.Now().Unix();signature:=fmt.Sprintf("t=%d,v1=%s",now,signPayload(s.Config.DemoSecret,now,body))
 result,err:=s.receiveDemo(r.Context(),body,signature);if err!=nil{fail(w,err);return};respond(w,202,result)
}
func (s *Server) DestinationHandler()http.Handler {
 mux:=http.NewServeMux()
 mux.HandleFunc("GET /healthz",func(w http.ResponseWriter,r *http.Request){respond(w,200,map[string]bool{"ok":true})})
 mux.HandleFunc("GET /readyz",func(w http.ResponseWriter,r *http.Request){ctx,cancel:=context.WithTimeout(r.Context(),2*time.Second);defer cancel();if err:=s.DB.Call(ctx,"ping",map[string]any{},nil);err!=nil{fail(w,err);return};respond(w,200,map[string]bool{"ready":true})})
 mux.HandleFunc("POST /orders",func(w http.ResponseWriter,r *http.Request){
  if !hmac.Equal([]byte(r.Header.Get("Authorization")),[]byte("Bearer "+s.Config.DestinationToken)){respond(w,401,map[string]string{"error":"Invalid destination credential"});return}
  key:=r.Header.Get("Idempotency-Key");if !uuidPattern.MatchString(key){respond(w,400,map[string]string{"error":"A delivery UUID is required as Idempotency-Key"});return}
  body,err:=readBody(w,r);if err!=nil{fail(w,err);return}
  var order Order;if json.Unmarshal(body,&order)!=nil||order.Validate()!=nil{respond(w,422,map[string]string{"error":"Invalid order envelope"});return}
  var result struct{Status int `json:"http_status"`;Duplicate bool `json:"duplicate"`;Stale bool `json:"ignored_stale"`;Error string `json:"error"`}
  if err:=s.DB.Call(r.Context(),"accept_delivery",map[string]any{"p_key":key,"p_payload":json.RawMessage(body)},&result);err!=nil{fail(w,err);return}
  if result.Status==503{w.Header().Set("Retry-After","2")};respond(w,result.Status,result)
 })
 return securityHeaders(mux)
}
