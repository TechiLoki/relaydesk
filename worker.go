package main
import (
 "bytes"
 "context"
 "encoding/json"
 "fmt"
 "io"
 "log/slog"
 "math"
 "math/rand/v2"
 "net/http"
 "strconv"
 "time"
)
type Job struct {ID string `json:"id"`;OrderID string `json:"order_id"`;Token string `json:"token"`;Attempt int `json:"attempt"`;Payload json.RawMessage `json:"payload"`}
type Worker struct {DB Database;Client *http.Client;URL,Token string}
func retryable(status int) bool {return status==0||status==408||status==425||status==429||status>=500}
func retryDelay(attempt int,retryAfter string,now time.Time) float64 {
 delay:=math.Min(60,math.Pow(2,float64(min(attempt,6))))+rand.Float64()
 if seconds,err:=strconv.Atoi(retryAfter);err==nil&&seconds>=0 {return math.Min(300,math.Max(delay,float64(seconds)))}
 if date,err:=http.ParseTime(retryAfter);err==nil {delay=math.Max(delay,date.Sub(now).Seconds())}
 return math.Min(300,delay)
}
func (w *Worker) Step(ctx context.Context)(bool,error) {
 var job *Job
 if err:=w.DB.Call(ctx,"claim_delivery",map[string]any{},&job);err!=nil{return false,err}
 if job==nil{return false,nil}
 started:=time.Now();status:=0;retryAfter:="";var failure any
 req,err:=http.NewRequestWithContext(ctx,"POST",w.URL,bytes.NewReader(job.Payload))
 if err==nil {
  req.Header.Set("Content-Type","application/json");req.Header.Set("Authorization","Bearer "+w.Token);req.Header.Set("Idempotency-Key",job.ID)
  var res *http.Response
  res,err=w.Client.Do(req)
  if err==nil {
   status=res.StatusCode;retryAfter=res.Header.Get("Retry-After")
   _,readErr:=io.Copy(io.Discard,io.LimitReader(res.Body,65536));res.Body.Close()
   if readErr!=nil{err=readErr}
  }
 }
 if err!=nil {failure="Destination request failed or timed out";status=0} else if status<200||status>=300 {failure=fmt.Sprintf("Destination returned HTTP %d",status)}
 var committed bool
 delay:=retryDelay(job.Attempt,retryAfter,time.Now())
 // A new short context allows a failed/cancelled delivery to be recorded. A hard
 // process crash still recovers via the expiring lease and destination idempotency.
 finishCtx,cancel:=context.WithTimeout(context.WithoutCancel(ctx),5*time.Second);defer cancel()
 err=w.DB.Call(finishCtx,"finish_delivery",map[string]any{"p_id":job.ID,"p_token":job.Token,"p_http_status":status,"p_duration_ms":int(time.Since(started).Milliseconds()),"p_error":failure,"p_retryable":retryable(status),"p_delay_seconds":delay},&committed)
 if err!=nil{return true,err}
 if !committed {slog.Warn("stale delivery completion discarded","delivery",job.ID)}
 return true,nil
}
func (w *Worker) Run(ctx context.Context) {
 for ctx.Err()==nil {
  worked,err:=w.Step(ctx)
  if err!=nil {slog.Error("worker iteration failed","error",err)}
  if !worked||err!=nil {select{case <-ctx.Done():return;case <-time.After(time.Second):}}
 }
}
