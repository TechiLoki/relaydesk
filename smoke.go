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
 "time"
)
func smoke(ctx context.Context,base,token string)error {
 client:=&http.Client{Timeout:10*time.Second};base=strings.TrimRight(base,"/")
 call:=func(method,path string,data any,out any)error{
  var body io.Reader
  if data!=nil{b,err:=json.Marshal(data);if err!=nil{return err};body=bytes.NewReader(b)}
  req,err:=http.NewRequestWithContext(ctx,method,base+path,body);if err!=nil{return err}
  req.Header.Set("Authorization","Bearer "+token);req.Header.Set("Content-Type","application/json")
  res,err:=client.Do(req);if err!=nil{return err};defer res.Body.Close()
  if res.StatusCode>=300{return fmt.Errorf("%s returned HTTP %d",path,res.StatusCode)}
  if out!=nil{return json.NewDecoder(res.Body).Decode(out)};return nil
 }
 setMode:=func(mode string)error{return call("POST","/api/simulator",map[string]string{"mode":mode},nil)}
 defer func(){_ = setMode("healthy")}()
 if err:=setMode("lost_ack");err!=nil{return err}
 var receipt IngestResult
 if err:=call("POST","/api/demo",map[string]string{"action":"new"},&receipt);err!=nil{return err}
 replays:=make(chan error,8)
 for i:=0;i<8;i++{go func(){replays<-call("POST","/api/demo",map[string]string{"action":"replay"},nil)}()}
 for i:=0;i<8;i++{if err:=<-replays;err!=nil{return err}}
 type details struct {
  Order struct{Version int `json:"version"`} `json:"order"`
  Deliveries []struct{ID,Status string;AttemptCount int `json:"attempt_count"`} `json:"deliveries"`
  Destination *struct{ID string;Version int} `json:"destination"`
  Events []struct{DuplicateCount int `json:"duplicate_count"`} `json:"events"`
  Audit []struct{Action string} `json:"audit"`
 }
 waitFor:=func(id,target string)(details,error){
  deadline:=time.Now().Add(90*time.Second)
  for time.Now().Before(deadline){
   var d details
   if err:=call("GET","/api/orders/"+id,nil,&d);err!=nil{return d,err}
   if len(d.Deliveries)>0&&d.Deliveries[0].Status==target{return d,nil}
   select{case<-ctx.Done():return d,ctx.Err();case<-time.After(500*time.Millisecond):}
  }
  return details{},errors.New("timed out waiting for "+target)
 }
 first,err:=waitFor(receipt.OrderID,"succeeded");if err!=nil{return err}
 if first.Destination==nil||len(first.Deliveries)!=1||first.Deliveries[0].AttemptCount<2||len(first.Events)!=1||first.Events[0].DuplicateCount!=8{return errors.New("lost-ack or duplicate-event proof failed")}
 commits:=0;for _,a:=range first.Audit{if a.Action=="destination_committed"{commits++}}
 if commits!=1{return errors.New("destination committed more than once")}
 destinationID:=first.Destination.ID
 if err:=setMode("healthy");err!=nil{return err}
 if err:=call("POST","/api/demo",map[string]string{"action":"update"},nil);err!=nil{return err}
 updated,err:=waitFor(receipt.OrderID,"succeeded");if err!=nil{return err}
 if updated.Destination==nil||updated.Destination.Version!=2||updated.Destination.ID!=destinationID||len(updated.Deliveries)!=2{return errors.New("version update did not preserve one destination order")}
 if err:=setMode("outage");err!=nil{return err}
 var failedReceipt IngestResult
 if err:=call("POST","/api/demo",map[string]string{"action":"new"},&failedReceipt);err!=nil{return err}
 failed,err:=waitFor(failedReceipt.OrderID,"failed");if err!=nil{return err}
 if failed.Destination!=nil||failed.Deliveries[0].AttemptCount!=5{return errors.New("outage retry budget proof failed")}
 if err:=setMode("healthy");err!=nil{return err}
 if err:=call("POST","/api/deliveries/"+failed.Deliveries[0].ID+"/retry",map[string]any{},nil);err!=nil{return err}
 recovered,err:=waitFor(failedReceipt.OrderID,"succeeded");if err!=nil{return err}
 if recovered.Destination==nil||recovered.Deliveries[0].AttemptCount!=6{return errors.New("manual retry did not preserve attempt history")}
 fmt.Printf("PASS: duplicate replay, lost acknowledgement, one destination write, version update, five-attempt backoff, manual recovery. Orders: %s, %s\n",receipt.OrderID,failedReceipt.OrderID)
 return nil
}
