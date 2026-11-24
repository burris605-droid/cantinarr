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
