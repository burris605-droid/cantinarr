package push

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/go-chi/chi/v5"
)

func TestV2QueueAndInboxIsolation(t *testing.T) {
	db,err:=dbOpen(t)
	if err!=nil {t.Fatal(err)}
	mustExec(t,db,"INSERT INTO users (id,username,password_hash,role) VALUES (1,'admin','', 'admin'),(2,'alice','','user'),(3,'bob','','user')")
	h:=NewHandler(db,nil,nil)
	ctx:=context.Background()
	req:=announcementV2Request{announcementRequest:announcementRequest{Audience:"selected",UserIDs:[]int64{2},Title:"Test",Body:"Private"},Key:"test-announcement-key-001",Mode:"now"}
	payload,_:=json.Marshal(req)
	save:=func(role string) *httptest.ResponseRecorder {
		r:=httptest.NewRequest(http.MethodPost,"/",strings.NewReader(string(payload)))
		r=r.WithContext(context.WithValue(r.Context(),auth.ClaimsKey,&auth.Claims{UserID:1,Role:role}))
		w:=httptest.NewRecorder();h.V2Save(w,r);return w
	}
	if w:=save("user");w.Code!=403 {t.Fatalf("nonadmin save %d",w.Code)}
	for i:=0;i<2;i++ {if w:=save("admin");w.Code!=200 {t.Fatalf("save %d: %s",w.Code,w.Body.String())}}
	h.v2RunOne(ctx)
	h.v2RunOne(ctx)
	var n int
	if err:=db.QueryRow("SELECT COUNT(*) FROM burris_v2_jobs").Scan(&n);err!=nil || n!=1 {t.Fatalf("queue duplicates: %d %v",n,err)}
	read:=func(id int64) string {
		w:=httptest.NewRecorder();h.V2Inbox(w,withUser(httptest.NewRequest(http.MethodGet,"/",nil),id))
		if w.Code!=200 {t.Fatalf("inbox %d: %s",w.Code,w.Body.String())};return w.Body.String()
	}
	if !strings.Contains(read(2),"Private") || strings.Contains(read(3),"Private") || strings.Contains(read(1),"Private") {t.Fatal("incorrect inbox scope")}
	var status string
	if err:=db.QueryRow("SELECT status FROM burris_v2_jobs").Scan(&status);err!=nil || status!="inbox_only" {t.Fatalf("status %s %v",status,err)}
	w:=httptest.NewRecorder();h.V2Clear(w,httptest.NewRequest(http.MethodDelete,"/",nil))
	if w.Code!=200 || !strings.Contains(read(2),"Private"){t.Fatal("history deletion affected inbox")}
}

func TestV2ScheduleValidation(t *testing.T) {
	now:=time.Now().Unix()
	a:=announcementV2Request{announcementRequest:announcementRequest{Audience:"admins",Title:"Hello",Body:"World"},Key:"schedule-test-key-001",Mode:"scheduled",RunAt:now+60,RepeatDays:7}
	if !a.validate(now){t.Fatal("valid schedule rejected")}
	a.RunAt=now-1
	if a.validate(now){t.Fatal("past schedule accepted")}
	a.RunAt=now+60;a.RepeatDays=2
	if a.validate(now){t.Fatal("unsupported recurrence accepted")}
}

func TestV2WelcomeDoesNotTargetExistingAccounts(t *testing.T) {
	db,err:=dbOpen(t);if err!=nil{t.Fatal(err)}
	mustExec(t,db,"INSERT INTO users (id,username,password_hash,role) VALUES (1,'admin','','admin'),(2,'existing','','user')")
	h:=NewHandler(db,nil,nil)
	payload:="{\"retention\":true,\"welcome\":true,\"title\":\"Welcome\",\"body\":\"Enjoy\"}"
	r:=httptest.NewRequest(http.MethodPut,"/",strings.NewReader(payload))
	r=r.WithContext(context.WithValue(r.Context(),auth.ClaimsKey,&auth.Claims{UserID:1,Role:"admin"}))
	w:=httptest.NewRecorder();h.V2Options(w,r)
	if w.Code!=200{t.Fatalf("options %d %s",w.Code,w.Body.String())}
	var n int
	if err=db.QueryRow("SELECT COUNT(*) FROM burris_v2_welcomed").Scan(&n);err!=nil||n!=2{t.Fatalf("existing users not excluded: %d %v",n,err)}
	h.v2Maintenance(context.Background())
	if err=db.QueryRow("SELECT COUNT(*) FROM burris_v2_jobs").Scan(&n);err!=nil||n!=0{t.Fatalf("unexpected retroactive welcome: %d %v",n,err)}
}


func TestV2InboxInteractionAuthorization(t *testing.T) {
	db,err:=dbOpen(t);if err!=nil{t.Fatal(err)}
	mustExec(t,db,"INSERT INTO users (id,username,password_hash,role) VALUES (1,'admin','','admin'),(2,'alice','','user'),(3,'bob','','user')")
	h:=NewHandler(db,nil,nil)
	ctx:=context.Background()
	if err=h.v2Schema(ctx);err!=nil{t.Fatal(err)}
	mustExec(t,db,"INSERT INTO burris_v2_inbox(id,job_id,user_id,title,body,created) VALUES(21,'job',2,'Test','Private',100),(22,'job',3,'Test','Private',100)")
	mustExec(t,db,"INSERT INTO burris_v2_inbox_meta(job_id,pinned) VALUES('job',1)")
	mustExec(t,db,"INSERT INTO burris_v2_interaction_meta(job_id,priority,require_ack) VALUES('job','urgent',1)")
	call:=func(handler http.HandlerFunc,method,id,body string,uid int64,role string)*httptest.ResponseRecorder{
		r:=httptest.NewRequest(method,"/",strings.NewReader(body))
		rc:=chi.NewRouteContext();rc.URLParams.Add("id",id)
		r=r.WithContext(context.WithValue(context.WithValue(r.Context(),chi.RouteCtxKey,rc),auth.ClaimsKey,&auth.Claims{UserID:uid,Role:role}))
		w:=httptest.NewRecorder();handler(w,r);return w
	}
	if w:=call(h.V2Replies,"POST","21","{\"body\":\"not mine\"}",3,"user");w.Code!=409{t.Fatalf("cross-user reply: %d",w.Code)}
	if w:=call(h.V2Replies,"POST","21","{\"body\":\"private response\"}",2,"user");w.Code!=200{t.Fatalf("own reply: %d %s",w.Code,w.Body.String())}
	if w:=call(h.V2Replies,"GET","21","",3,"user");w.Code!=200||strings.Contains(w.Body.String(),"private response"){t.Fatal("cross-user reply disclosed")}
	if w:=call(h.V2AdminReplies,"GET","21","",3,"user");w.Code!=403{t.Fatal("admin route not restricted")}
	if w:=call(h.V2AdminReplies,"POST","21","{\"body\":\"admin response\"}",1,"admin");w.Code!=200{t.Fatalf("admin reply: %d",w.Code)}
	if w:=call(h.V2Replies,"GET","21","",2,"user");!strings.Contains(w.Body.String(),"admin response"){t.Fatal("admin reply missing")}
	if w:=call(h.V2Acknowledge,"POST","21","",3,"user");w.Code!=404{t.Fatal("cross-user acknowledgment accepted")}
	if w:=call(h.V2Acknowledge,"POST","21","",2,"user");w.Code!=200{t.Fatalf("own acknowledgment: %d",w.Code)}
	if w:=call(h.V2Inbox,"DELETE","21","",2,"user");w.Code!=409{t.Fatal("pinned message deleted")}
	if w:=call(h.V2InboxRecall,"DELETE","job","",2,"user");w.Code!=403{t.Fatal("user recall accepted")}
	if w:=call(h.V2InboxRecall,"DELETE","job","",1,"admin");w.Code!=200{t.Fatalf("admin recall: %d",w.Code)}
	var n int
	if err=db.QueryRow("SELECT COUNT(*) FROM burris_v2_inbox").Scan(&n);err!=nil||n!=0{t.Fatalf("recall did not remove copies: %d %v",n,err)}
}
