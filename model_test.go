package main
import (
 "encoding/json"
 "fmt"
 "os"
 "strconv"
 "strings"
 "testing"
 "time"
)
func TestSignatureVerification(t *testing.T){
 now:=time.Unix(1800000000,0);body:=[]byte(`{"id":"evt_1"}`);secret:="test-secret"
 signature:=fmt.Sprintf("t=%d,v1=%s",now.Unix(),signPayload(secret,now.Unix(),body))
 if err:=verifySignature(secret,signature,body,now);err!=nil{t.Fatal(err)}
 for _,test:=range []struct{name,secret,header string;body []byte}{
  {"tampered",secret,signature,[]byte(`{"id":"evt_2"}`)},
  {"wrong-secret","wrong",signature,body},
  {"missing-secret","",signature,body},
  {"expired",secret,fmt.Sprintf("t=%d,v1=%s",now.Unix()-301,signPayload(secret,now.Unix()-301,body)),body},
  {"future",secret,fmt.Sprintf("t=%d,v1=%s",now.Unix()+301,signPayload(secret,now.Unix()+301,body)),body},
  {"missing-timestamp",secret,"v1=abc",body},
  {"duplicate-timestamp",secret,signature+",t="+strconv.FormatInt(now.Unix(),10),body},
 } {t.Run(test.name,func(t *testing.T){if verifySignature(test.secret,test.header,test.body,now)==nil{t.Fatal("invalid signature accepted")}})}
 if err:=verifySignature(secret,signature+",v1=old-signature",body,now);err!=nil{t.Fatal("valid rotation signature rejected",err)}
}
func TestDemoValidation(t *testing.T){
 e:=sampleEvent();body,_:=json.Marshal(e);event,err:=parseDemo(body)
 if err!=nil||event.Order.Source!="demo"{t.Fatal(err)}
 cases:=[]struct{name string;change func(*DemoEvent)}{
  {"negative quantity",func(e *DemoEvent){e.Order.Items[0].Quantity=-1}},
  {"fraction unsupported by JSON integer",func(e *DemoEvent){e.Order.Version=0}},
  {"invalid currency",func(e *DemoEvent){e.Order.Currency="usd"}},
  {"empty items",func(e *DemoEvent){e.Order.Items=nil}},
  {"invalid identifier",func(e *DemoEvent){e.Order.ExternalID="bad/id"}},
  {"negative amount",func(e *DemoEvent){e.Order.Amount=-1}},
 }
 for _,tc:=range cases{t.Run(tc.name,func(t *testing.T){event:=sampleEvent();tc.change(&event);body,_:=json.Marshal(event);if _,err:=parseDemo(body);err==nil{t.Fatal("invalid order accepted")}})}
 if _,err:=parseDemo([]byte(`{"id":"x","type":"order.upsert","order":{"version":1.5}}`));err==nil{t.Fatal("fractional version accepted")}
}
func TestStripeTestModeAndBusinessIdentity(t *testing.T){
 body:=[]byte(`{"id":"evt_123","type":"checkout.session.completed","livemode":false,"data":{"object":{"id":"cs_test_123","payment_status":"paid","amount_total":3900,"currency":"usd","customer_details":{"name":"Test Buyer","email":"buyer@example.test"}}}}`)
 id,order,err:=parseStripe(body)
 if err!=nil||id!="evt_123"||order==nil||order.Currency!="USD"||order.Version!=1||len(order.Items)!=0{t.Fatalf("bad normalized checkout: %+v %v",order,err)}
 async:=strings.ReplaceAll(string(body),"checkout.session.completed","checkout.session.async_payment_succeeded")
 _,second,err:=parseStripe([]byte(async));if err!=nil||second.ExternalID!=order.ExternalID||second.Version!=order.Version{t.Fatal("equivalent paid notification changed identity")}
 for _,replacement:=range []string{strings.ReplaceAll(string(body),`"livemode":false`,`"livemode":true`),strings.ReplaceAll(string(body),`"amount_total":3900`,`"amount_total":null`)}{if _,_,err:=parseStripe([]byte(replacement));err==nil{t.Fatal("invalid Stripe order accepted")}}
 unpaid:=strings.ReplaceAll(string(body),`"paid"`,`"unpaid"`);if _,o,err:=parseStripe([]byte(unpaid));err!=nil||o!=nil{t.Fatal("unpaid checkout should be ignored")}
 unrelated:=strings.ReplaceAll(string(body),"checkout.session.completed","customer.created");if _,o,err:=parseStripe([]byte(unrelated));err!=nil||o!=nil{t.Fatal("unrelated event should be ignored")}
}
func TestNativeRulesContract(t *testing.T){
 binary:=os.Getenv("RULES_BIN");if binary==""{t.Skip("set RULES_BIN to the compiled C++ validator; Docker checks do this")}
 order:=sampleEvent().Order
 if err:=(NativeRules{binary}).Check(t.Context(),&order);err!=nil{t.Fatal(err)}
 if order.Validation.Subtotal!=6000||order.Validation.Total!=6800||!order.Validation.CheckedLines{t.Fatalf("wrong reconciliation: %+v",order.Validation)}
 order.Amount++
 if (NativeRules{binary}).Check(t.Context(),&order)==nil{t.Fatal("inconsistent total accepted")}
}

func TestEventFingerprintIgnoresWhitespaceAndFieldOrder(t *testing.T){
 first:=[]byte(`{"id":"evt_1","order":{"version":1,"amount_minor":100}}`)
 second:=[]byte(`{ "order": {"amount_minor":100, "version":1}, "id":"evt_1" }`)
 if eventDigest(first)!=eventDigest(second){t.Fatal("equivalent JSON was treated as conflicting content")}
 if eventDigest(first)==eventDigest([]byte(`{"id":"evt_1","order":{"version":2,"amount_minor":100}}`)){t.Fatal("changed payload did not change fingerprint")}
}
