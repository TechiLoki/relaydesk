package main
import (
 "bytes"
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "strings"
)
type Database interface {Call(context.Context,string,any,any) error}
type RESTStore struct {URL string;Client *http.Client}
type DBError struct {Status int;Message string}
func (e *DBError) Error() string{return e.Message}
func (s *RESTStore) Call(ctx context.Context,name string,input any,out any) error {
 body,err:=json.Marshal(input);if err!=nil{return err}
 req,err:=http.NewRequestWithContext(ctx,"POST",strings.TrimRight(s.URL,"/")+"/rpc/"+name,bytes.NewReader(body));if err!=nil{return err}
 req.Header.Set("Content-Type","application/json")
 res,err:=s.Client.Do(req);if err!=nil{return fmt.Errorf("database API is unavailable")};defer res.Body.Close()
 data,err:=io.ReadAll(io.LimitReader(res.Body,2*1024*1024));if err!=nil{return fmt.Errorf("database API read failed")}
 if res.StatusCode>=300 {
  status:=http.StatusServiceUnavailable;message:="database operation failed"
  var pg struct{Message string `json:"message"`;Code string `json:"code"`};_ = json.Unmarshal(data,&pg)
  if pg.Code=="23505" {status=409;message="event ID or order version conflicts with different content"}
  if pg.Code=="P0002" {status=404;message="record not found"}
  if pg.Code=="22023" {status=422;message=pg.Message}
  return &DBError{status,message}
 }
 if out!=nil&&len(bytes.TrimSpace(data))>0 {return json.Unmarshal(data,out)}
 return nil
}
type IngestResult struct {OrderID string `json:"order_id"`;EventID string `json:"event_id"`;Status string `json:"status"`;Duplicate bool `json:"duplicate"`}
func ingest(ctx context.Context,db Database,rules Reconciler,source,eventID string,body []byte,order *Order,demo json.RawMessage)(IngestResult,error) {
 var result IngestResult
 if err:=rules.Check(ctx,order);err!=nil{if errors.Is(err,errRulesUnavailable){return result,&DBError{503,err.Error()}};return result,&DBError{422,err.Error()}}
 err:=db.Call(ctx,"ingest_event",map[string]any{"p_source":source,"p_event_id":eventID,"p_body_hash":eventDigest(body),"p_order":order,"p_demo_event":demo},&result)
 return result,err
}
