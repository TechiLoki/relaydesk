package main

import (
 "crypto/hmac"
 "crypto/rand"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "errors"
 "fmt"
 "regexp"
 "strconv"
 "strings"
 "time"
)

const maxMoney int64 = 1_000_000_000_000
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,160}$`)

type Item struct {
 SKU string `json:"sku"`
 Name string `json:"name"`
 Quantity int64 `json:"quantity"`
 UnitAmount int64 `json:"unit_amount_minor"`
}
type Totals struct {
 Subtotal int64 `json:"subtotal_minor"`
 Total int64 `json:"total_minor"`
 Units int64 `json:"units"`
 CheckedLines bool `json:"checked_lines"`
}
type Order struct {
 Source string `json:"source"`
 ExternalID string `json:"external_id"`
 Version int64 `json:"version"`
 Customer string `json:"customer"`
 Email string `json:"email"`
 Currency string `json:"currency"`
 Amount int64 `json:"amount_minor"`
 Shipping int64 `json:"shipping_minor"`
 Tax int64 `json:"tax_minor"`
 Discount int64 `json:"discount_minor"`
 Items []Item `json:"items"`
 Validation Totals `json:"validation"`
}
type DemoEvent struct {
 ID string `json:"id"`
 Type string `json:"type"`
 Order Order `json:"order"`
}
func newID() string {
 b:=make([]byte,16)
 if _,err:=rand.Read(b);err!=nil { panic("secure randomness unavailable") }
 return hex.EncodeToString(b)
}
func digest(b []byte) string { sum:=sha256.Sum256(b);return hex.EncodeToString(sum[:]) }
// Fingerprint semantic JSON, while signature verification still uses exact wire bytes.
func eventDigest(body []byte) string {
 var value any
 decoder:=json.NewDecoder(strings.NewReader(string(body)));decoder.UseNumber()
 if decoder.Decode(&value)!=nil{return digest(body)}
 canonical,err:=json.Marshal(value);if err!=nil{return digest(body)}
 return digest(canonical)
}
func signPayload(secret string,timestamp int64,body []byte) string {
 mac:=hmac.New(sha256.New,[]byte(secret));fmt.Fprintf(mac,"%d.",timestamp);mac.Write(body)
 return hex.EncodeToString(mac.Sum(nil))
}
func verifySignature(secret,header string,body []byte,now time.Time) error {
 if secret=="" {return errors.New("webhook signing secret is not configured")}
 var timestamp string;var signatures []string
 for _,part:=range strings.Split(header,",") {
  pair:=strings.SplitN(strings.TrimSpace(part),"=",2)
  if len(pair)!=2 {continue}
  if pair[0]=="t" {if timestamp!="" {return errors.New("multiple signature timestamps")};timestamp=pair[1]}
  if pair[0]=="v1" {signatures=append(signatures,pair[1])}
 }
 seconds,err:=strconv.ParseInt(timestamp,10,64)
 // Subtraction is bounded by realistic timestamps before conversion to Duration.
 if err!=nil || seconds<now.Unix()-300 || seconds>now.Unix()+300 {return errors.New("signature timestamp is missing or expired")}
 expected:=signPayload(secret,seconds,body)
 for _,signature:=range signatures {if hmac.Equal([]byte(expected),[]byte(signature)){return nil}}
 return errors.New("webhook signature is invalid")
}
func (o *Order) Validate() error {
 if o.Source!="demo"&&o.Source!="stripe" {return errors.New("unsupported source")}
 if !idPattern.MatchString(o.ExternalID)||o.Version<1 {return errors.New("invalid order ID or version")}
 if !currencyPattern.MatchString(o.Currency) {return errors.New("currency must be a three-letter uppercase code")}
 if len(o.Customer)>160||len(o.Email)>254 {return errors.New("customer fields are too long")}
 for _,value:=range []int64{o.Amount,o.Shipping,o.Tax,o.Discount} {if value<0||value>maxMoney{return errors.New("amounts must be non-negative minor-unit integers within the supported limit")}}
 if len(o.Items)>500 {return errors.New("maximum 500 line items")}
 if o.Source=="demo"&&len(o.Items)==0{return errors.New("demo orders require line items")}
 for _,item:=range o.Items {
  if !idPattern.MatchString(item.SKU)||len(item.Name)>200||item.Quantity<1||item.Quantity>10000||item.UnitAmount<0||item.UnitAmount>maxMoney {return errors.New("invalid line item")}
 }
 return nil
}
func parseDemo(body []byte) (DemoEvent,error) {
 var e DemoEvent
 if err:=json.Unmarshal(body,&e);err!=nil{return e,errors.New("invalid demo event JSON")}
 if !idPattern.MatchString(e.ID)||e.Type!="order.upsert" {return e,errors.New("expected an order.upsert event with an ID")}
 // Source and reconciliation results always come from the receiver, not the sender.
 e.Order.Source="demo";e.Order.Validation=Totals{}
 return e,e.Order.Validate()
}
type StripeEvent struct {
 ID string `json:"id"`
 Type string `json:"type"`
 Created int64 `json:"created"`
 Live bool `json:"livemode"`
 Data struct {Object struct {
  ID string `json:"id"`
  PaymentStatus string `json:"payment_status"`
  Amount *int64 `json:"amount_total"`
  Currency string `json:"currency"`
  Customer struct {Name string `json:"name"`;Email string `json:"email"`} `json:"customer_details"`
 } `json:"object"`} `json:"data"`
}
func parseStripe(body []byte) (string,*Order,error) {
 var e StripeEvent
 if json.Unmarshal(body,&e)!=nil||!idPattern.MatchString(e.ID){return "",nil,errors.New("invalid Stripe snapshot event")}
 if e.Live{return "",nil,errors.New("only Stripe test-mode events are supported")}
 if e.Type!="checkout.session.completed"&&e.Type!="checkout.session.async_payment_succeeded"{return e.ID,nil,nil}
 if e.Data.Object.PaymentStatus!="paid"{return e.ID,nil,nil}
 session:=e.Data.Object
 if session.Amount==nil{return "",nil,errors.New("Stripe session is missing amount_total")}
 // These paid notifications are the same business state. Their Stripe event IDs
 // can differ, but the session ID and paid-state version prevent a second delivery.
 o:=&Order{Source:"stripe",ExternalID:session.ID,Version:1,Customer:session.Customer.Name,Email:session.Customer.Email,Currency:strings.ToUpper(session.Currency),Amount:*session.Amount,Items:[]Item{}}
 return e.ID,o,o.Validate()
}
func sampleEvent() DemoEvent {
 o:=Order{Source:"demo",ExternalID:"RD-"+strings.ToUpper(newID()[:8]),Version:1,Customer:"Morgan Reed",Email:"morgan@example.test",Currency:"USD",Amount:6800,Shipping:500,Tax:300,Discount:0,Items:[]Item{{SKU:"NOTE-01",Name:"Studio notebook",Quantity:2,UnitAmount:1800},{SKU:"PEN-03",Name:"Fine-tip pen set",Quantity:1,UnitAmount:2400}}}
 return DemoEvent{ID:"evt_demo_"+newID(),Type:"order.upsert",Order:o}
}
