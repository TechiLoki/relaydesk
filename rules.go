package main
import (
 "bytes"
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "os/exec"
 "strings"
 "time"
)
var errRulesUnavailable=errors.New("native order validator is unavailable")

type Reconciler interface {Check(context.Context,*Order) error}
type NativeRules struct {Binary string}
func (rules NativeRules) Check(ctx context.Context,o *Order) error {
 ctx,cancel:=context.WithTimeout(ctx,2*time.Second);defer cancel()
 var input strings.Builder
 fmt.Fprintf(&input,"%d %d %d %d %d\n",o.Amount,o.Shipping,o.Tax,o.Discount,len(o.Items))
 for _,line:=range o.Items {fmt.Fprintf(&input,"%d %d\n",line.Quantity,line.UnitAmount)}
 cmd:=exec.CommandContext(ctx,rules.Binary);cmd.Stdin=strings.NewReader(input.String())
 var output,stderr bytes.Buffer;cmd.Stdout=&output;cmd.Stderr=&stderr
 if err:=cmd.Run();err!=nil {
  if ctx.Err()!=nil {return errRulesUnavailable}
  var exit *exec.ExitError
  if errors.As(err,&exit)&&exit.ExitCode()==2 {return fmt.Errorf("order totals rejected: %s",strings.TrimSpace(stderr.String()))}
  return errRulesUnavailable
 }
 if json.Unmarshal(output.Bytes(),&o.Validation)!=nil{return errRulesUnavailable}
 if o.Validation.Total!=o.Amount{return errRulesUnavailable}
 return nil
}
