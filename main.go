package main
import (
 "context"
 "embed"
 "flag"
 "fmt"
 "io/fs"
 "log/slog"
 "net/http"
 "os"
 "os/signal"
 "syscall"
 "time"
)
//go:embed web/dist/*
var webFiles embed.FS
func env(name,fallback string)string{if v:=os.Getenv(name);v!=""{return v};return fallback}
func main(){
 mode:=flag.String("mode","api","api, worker, destination, smoke or health")
 addr:=flag.String("addr",":8080","HTTP bind address")
 flag.Parse()
 if *mode=="health"{client:=&http.Client{Timeout:2*time.Second};res,err:=client.Get("http://127.0.0.1:8080/readyz");if err!=nil||res.StatusCode!=200{os.Exit(1)};res.Body.Close();return}
 ctx,stop:=signal.NotifyContext(context.Background(),os.Interrupt,syscall.SIGTERM);defer stop()
 db:=&RESTStore{URL:env("POSTGREST_URL","http://rest:3000"),Client:&http.Client{Timeout:5*time.Second}}
 config:=Config{AdminToken:env("ADMIN_TOKEN","relaydesk-local-admin"),DemoSecret:env("DEMO_WEBHOOK_SECRET","relaydesk-demo-signing-secret"),StripeSecret:os.Getenv("STRIPE_WEBHOOK_SECRET"),DestinationToken:env("DESTINATION_TOKEN","relaydesk-local-destination"),SecureCookies:os.Getenv("COOKIE_SECURE")=="true"}
 if *mode=="worker"{
  worker:=Worker{DB:db,URL:env("DESTINATION_URL","http://destination:8080/orders"),Token:config.DestinationToken,Client:&http.Client{Timeout:8*time.Second,CheckRedirect:func(req *http.Request,via []*http.Request)error{return http.ErrUseLastResponse}}}
  worker.Run(ctx);return
 }
 if *mode=="smoke"{if err:=smoke(ctx,env("APP_URL","http://api:8080"),config.AdminToken);err!=nil{slog.Error("live smoke failed","error",err);os.Exit(1)};return}
 assets,_:=fs.Sub(webFiles,"web/dist")
 app:=&Server{DB:db,Rules:NativeRules{Binary:env("RULES_BIN","/usr/local/bin/order-rules")},Config:config,Assets:assets}
 handler:=app.Handler()
 if *mode=="destination"{handler=app.DestinationHandler()}else if *mode!="api"{fmt.Fprintln(os.Stderr,"Unknown mode");os.Exit(2)}
 srv:=&http.Server{Addr:*addr,Handler:handler,ReadHeaderTimeout:5*time.Second,ReadTimeout:10*time.Second,WriteTimeout:20*time.Second,IdleTimeout:60*time.Second,MaxHeaderBytes:8192}
 go func(){<-ctx.Done();shutdown,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel();_ = srv.Shutdown(shutdown)}()
 slog.Info("service starting","mode",*mode,"address",*addr)
 if err:=srv.ListenAndServe();err!=nil&&err!=http.ErrServerClosed{slog.Error("server stopped","error",err);os.Exit(1)}
}
