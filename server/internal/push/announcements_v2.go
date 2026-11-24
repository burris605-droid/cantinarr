package push

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

// V2 is isolated from the original notification pipeline. A persistent queue
// claims each occurrence before contacting the gateway; interrupted sends are
// marked unknown, never automatically replayed.
type announcementV2Request struct {
	announcementRequest
	Key string `json:"key"`
	Mode string `json:"mode"`
	RunAt int64 `json:"run_at"`
	RepeatDays int `json:"repeat_days"`
}

func (a *announcementV2Request) validate(now int64) bool {
	if a.Audience=="admins" {
		if len(a.UserIDs)!=0 {return false}
		a.Audience="all"
		ok:=a.announcementRequest.valid()
		a.Audience="admins"
		if !ok {return false}
	} else if !a.announcementRequest.valid() {return false}
	if len(a.Key)<16 || len(a.Key)>128 || strings.ContainsAny(a.Key," \t\r\n/") {return false}
	if a.RepeatDays!=0 && a.RepeatDays!=1 && a.RepeatDays!=7 && a.RepeatDays!=30 {return false}
	switch a.Mode {
	case "now":
		a.RunAt=now
		return a.RepeatDays==0
	case "scheduled":
		return a.RunAt>now && a.RunAt<=now+366*86400
	case "draft","template":
		a.RunAt=0
		a.RepeatDays=0
		return true
	}
	return false
}

func (h *Handler) v2Schema(ctx context.Context) error {
	for _, q:=range []string{
		"CREATE TABLE IF NOT EXISTS burris_v2_jobs (id TEXT PRIMARY KEY, sender INTEGER NOT NULL, payload TEXT NOT NULL, status TEXT NOT NULL, run_at INTEGER NOT NULL, repeat_days INTEGER NOT NULL, created INTEGER NOT NULL, recipients INTEGER NOT NULL DEFAULT 0, skipped INTEGER NOT NULL DEFAULT 0, sent INTEGER NOT NULL DEFAULT 0, failed INTEGER NOT NULL DEFAULT 0, note TEXT NOT NULL DEFAULT '')",
		"CREATE INDEX IF NOT EXISTS burris_v2_due ON burris_v2_jobs(status,run_at)",
		"CREATE TABLE IF NOT EXISTS burris_v2_inbox (id INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, title TEXT NOT NULL, body TEXT NOT NULL, created INTEGER NOT NULL, read_at INTEGER NOT NULL DEFAULT 0, UNIQUE(job_id,user_id))",
		"CREATE INDEX IF NOT EXISTS burris_v2_inbox_user ON burris_v2_inbox(user_id,created)",
	} {
		if _,err:=h.db.ExecContext(ctx,q);err!=nil{return err}
	}
	return nil
}

func (h *Handler) v2Ready(w http.ResponseWriter,r *http.Request) bool {
	w.Header().Set("Cache-Control","no-store")
	if err:=h.v2Schema(r.Context());err!=nil {
		h.logger.Error("announcement v2 schema","err",err)
		writeJSON(w,503,map[string]string{"error":"announcement storage unavailable"})
		return false
	}
	return true
}

func (h *Handler) V2Save(w http.ResponseWriter,r *http.Request) {
	claims:=auth.GetClaims(r.Context())
	if claims==nil || !auth.HasPermission(claims.Role,auth.PermissionAdmin) {writeJSON(w,403,map[string]string{"error":"administrator required"});return}
	var a announcementV2Request
	dec:=json.NewDecoder(http.MaxBytesReader(w,r.Body,16384));dec.DisallowUnknownFields()
	now:=time.Now().Unix()
	if dec.Decode(&a)!=nil || dec.Decode(new(any))!=io.EOF || !a.validate(now) {writeJSON(w,400,map[string]string{"error":"invalid announcement or schedule"});return}
	if !h.v2Ready(w,r) {return}
	data,_:=json.Marshal(a)
	status:=a.Mode
	if status=="now" || status=="scheduled" {status="queued"}
	var err error
	if r.Method==http.MethodPut {
		id:=chi.URLParam(r,"id")
		if id!=a.Key {writeJSON(w,400,map[string]string{"error":"invalid key"});return}
		var result sql.Result
		result,err=h.db.ExecContext(r.Context(),"UPDATE burris_v2_jobs SET payload=?, status=?, run_at=?, repeat_days=? WHERE id=? AND sender=? AND status IN ('queued','draft','template')",string(data),status,a.RunAt,a.RepeatDays,id,claims.UserID)
		if err==nil {n,_:=result.RowsAffected();if n!=1 {writeJSON(w,409,map[string]string{"error":"message is no longer editable"});return}}
	} else {
		// Duplicate submissions are safe, but a key cannot be reused for a
		// different sender or a changed message.
		_,err=h.db.ExecContext(r.Context(),"INSERT INTO burris_v2_jobs(id,sender,payload,status,run_at,repeat_days,created) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING",a.Key,claims.UserID,string(data),status,a.RunAt,a.RepeatDays,now)
		if err==nil {
			var existing string;var sender int64
			err=h.db.QueryRowContext(r.Context(),"SELECT sender,payload,status FROM burris_v2_jobs WHERE id=?",a.Key).Scan(&sender,&existing,&status)
			var old announcementV2Request
			if err==nil && (json.Unmarshal([]byte(existing),&old)!=nil || sender!=claims.UserID || !sameV2Request(old,a)) {writeJSON(w,409,map[string]string{"error":"message key already used"});return}
		}
	}
	if err!=nil {h.logger.Error("announcement save","err",err);writeJSON(w,500,map[string]string{"error":"save failed"});return}
	writeJSON(w,200,map[string]any{"id":a.Key,"status":status})
}

func sameV2Request(a,b announcementV2Request) bool {
	if a.Mode=="now" && b.Mode=="now" {a.RunAt=b.RunAt}
	x,_:=json.Marshal(a);y,_:=json.Marshal(b)
	return string(x)==string(y)
}

func (h *Handler) V2List(w http.ResponseWriter,r *http.Request) {
	if !h.v2Ready(w,r) {return}
	rows,err:=h.db.QueryContext(r.Context(),"SELECT id,sender,payload,status,run_at,repeat_days,created,recipients,skipped,sent,failed,note FROM burris_v2_jobs ORDER BY CASE WHEN status IN ('queued','running','draft','template') THEN 0 ELSE 1 END, created DESC LIMIT 500")
	if err!=nil {writeJSON(w,500,map[string]string{"error":"history unavailable"});return}
	defer rows.Close()
	out:=[]map[string]any{}
	for rows.Next() {
		var id,payload,status,note string
		var sender,runAt,repeat,created,recipients,skipped,sent,failed int64
		if err=rows.Scan(&id,&sender,&payload,&status,&runAt,&repeat,&created,&recipients,&skipped,&sent,&failed,&note);err!=nil {break}
		var a announcementV2Request
		if err=json.Unmarshal([]byte(payload),&a);err!=nil {break}
		out=append(out,map[string]any{"id":id,"sender":sender,"title":a.Title,"body":a.Body,"audience":a.Audience,"user_ids":a.UserIDs,"status":status,"run_at":runAt,"repeat_days":repeat,"created":created,"recipients":recipients,"skipped":skipped,"sent":sent,"failed":failed,"note":note})
	}
	if err!=nil || rows.Err()!=nil {writeJSON(w,500,map[string]string{"error":"history unavailable"});return}
	writeJSON(w,200,out)
}

// Admin history is independent of the personal inbox. Never delete running
// jobs, and never clear schedules, drafts or templates in a history purge.
func (h *Handler) V2Delete(w http.ResponseWriter,r *http.Request) {
	if !h.v2Ready(w,r) {return}
	id:=chi.URLParam(r,"id")
	res,err:=h.db.ExecContext(r.Context(),"DELETE FROM burris_v2_jobs WHERE id=? AND status<>'running'",id)
	if err!=nil {writeJSON(w,500,map[string]string{"error":"delete failed"});return}
	n,_:=res.RowsAffected()
	if n!=1 {writeJSON(w,409,map[string]string{"error":"message is running or no longer exists"});return}
	writeJSON(w,200,map[string]bool{"success":true})
}
func (h *Handler) V2Clear(w http.ResponseWriter,r *http.Request) {
	if !h.v2Ready(w,r) {return}
	cutoff:=time.Now().Unix()
	if r.URL.Query().Get("older")=="30" {cutoff-=30*86400}
	if err:=h.announcementTable(r.Context());err!=nil {writeJSON(w,500,map[string]string{"error":"history unavailable"});return}
	tx,err:=h.db.BeginTx(r.Context(),nil)
	if err!=nil {writeJSON(w,500,map[string]string{"error":"history unavailable"});return}
	defer tx.Rollback()
	_,err=tx.ExecContext(r.Context(),"DELETE FROM burris_v2_jobs WHERE status IN ('completed','unknown','failed','inbox_only') AND created<=?",cutoff)
	if err==nil {_,err=tx.ExecContext(r.Context(),"DELETE FROM burris_announcements WHERE status<>'pending' AND strftime('%s',created)<=CAST(? AS TEXT)",cutoff)}
	if err==nil {err=tx.Commit()}
	if err!=nil {writeJSON(w,500,map[string]string{"error":"clear failed"});return}
	writeJSON(w,200,map[string]bool{"success":true})
}

type announcementCandidate struct {id int64; role string; tokens int}
func (h *Handler) v2Candidates(ctx context.Context) ([]announcementCandidate,error) {
	rows,err:=h.db.QueryContext(ctx,"SELECT u.id,u.role,(SELECT COUNT(*) FROM push_tokens p JOIN devices d ON d.id=p.device_id AND d.user_id=u.id WHERE p.user_id=u.id AND d.revoked_at IS NULL) FROM users u ORDER BY u.id")
	if err!=nil{return nil,err}
	defer rows.Close()
	var out []announcementCandidate
	for rows.Next(){var c announcementCandidate;if err:=rows.Scan(&c.id,&c.role,&c.tokens);err!=nil{return nil,err};out=append(out,c)}
	return out,rows.Err()
}
func (h *Handler) V2Recipients(w http.ResponseWriter,r *http.Request) {
	if !h.v2Ready(w,r){return}
	users,err:=h.v2Candidates(r.Context())
	if err!=nil {writeJSON(w,500,map[string]string{"error":"recipients unavailable"});return}
	p,err:=h.prefs.Policy()
	if err!=nil {writeJSON(w,500,map[string]string{"error":"preferences unavailable"});return}
	out:=[]map[string]any{}
	for _,u:=range users{
		prefs,e:=h.prefs.Get(u.id)
		if e!=nil {writeJSON(w,500,map[string]string{"error":"preferences unavailable"});return}
		out=append(out,map[string]any{"id":u.id,"devices":u.tokens,"eligible":p.Enabled&&prefs.PushEnabled&&u.tokens>0,"push_enabled":prefs.PushEnabled})
	}
	writeJSON(w,200,out)
}

func (h *Handler) V2Inbox(w http.ResponseWriter,r *http.Request) {
	claims:=auth.GetClaims(r.Context())
	if claims==nil {writeJSON(w,401,map[string]string{"error":"unauthorized"});return}
	if !h.v2Ready(w,r){return}
	if r.Method!=http.MethodGet {
		id:=chi.URLParam(r,"id")
		var err error
		if r.Method==http.MethodDelete {_,err=h.db.ExecContext(r.Context(),"DELETE FROM burris_v2_inbox WHERE id=? AND user_id=?",id,claims.UserID)
		} else if id=="all" {_,err=h.db.ExecContext(r.Context(),"UPDATE burris_v2_inbox SET read_at=? WHERE user_id=? AND read_at=0",time.Now().Unix(),claims.UserID)
		} else {_,err=h.db.ExecContext(r.Context(),"UPDATE burris_v2_inbox SET read_at=? WHERE id=? AND user_id=? AND read_at=0",time.Now().Unix(),id,claims.UserID)}
		if err!=nil {writeJSON(w,500,map[string]string{"error":"inbox update failed"});return}
		writeJSON(w,200,map[string]bool{"success":true});return
	}
	rows,err:=h.db.QueryContext(r.Context(),"SELECT id,title,body,created,read_at FROM burris_v2_inbox WHERE user_id=? ORDER BY created DESC,id DESC LIMIT 500",claims.UserID)
	if err!=nil {writeJSON(w,500,map[string]string{"error":"inbox unavailable"});return}
	defer rows.Close()
	out:=[]map[string]any{}
	for rows.Next(){var id,created,read int64;var title,body string;if err=rows.Scan(&id,&title,&body,&created,&read);err!=nil{break};out=append(out,map[string]any{"id":id,"title":title,"body":body,"created":created,"read":read>0})}
	if err!=nil || rows.Err()!=nil {writeJSON(w,500,map[string]string{"error":"inbox unavailable"});return}
	writeJSON(w,200,out)
}

// StartAnnouncementWorker is called once by the server at startup.
func (h *Handler) StartAnnouncementWorker(ctx context.Context) {
	if err:=h.v2Schema(ctx);err!=nil {h.logger.Error("announcement worker init","err",err);return}
	if _,err:=h.db.ExecContext(ctx,"UPDATE burris_v2_jobs SET status='unknown',note='Server interrupted send; not replayed' WHERE status='running'");err!=nil {h.logger.Error("announcement worker recovery","err",err);return}
	go func(){
		ticker:=time.NewTicker(3*time.Second);defer ticker.Stop()
		for {
			select {case <-ctx.Done():return;case <-ticker.C:h.v2RunOne(ctx)}
		}
	}()
}
func (h *Handler) v2RunOne(ctx context.Context) {
	now:=time.Now().Unix()
	var id,payload string;var sender,runAt int64;var repeat int
	err:=h.db.QueryRowContext(ctx,"SELECT id,sender,payload,run_at,repeat_days FROM burris_v2_jobs WHERE status='queued' AND run_at<=? ORDER BY run_at,created LIMIT 1",now).Scan(&id,&sender,&payload,&runAt,&repeat)
	if errors.Is(err,sql.ErrNoRows){return}
	if err!=nil {h.logger.Error("announcement queue","err",err);return}
	tx,err:=h.db.BeginTx(ctx,nil);if err!=nil{return};defer tx.Rollback()
	res,err:=tx.ExecContext(ctx,"UPDATE burris_v2_jobs SET status='running' WHERE id=? AND status='queued' AND payload=? AND run_at=?",id,payload,runAt)
	if err!=nil{return};n,_:=res.RowsAffected();if n!=1{return}
	var a announcementV2Request
	if err=json.Unmarshal([]byte(payload),&a);err!=nil {_,_=tx.ExecContext(ctx,"UPDATE burris_v2_jobs SET status='failed',note='Invalid saved request' WHERE id=?",id);_=tx.Commit();return}
	// Recheck sender at execution; no continuing recurring series from a
	// deleted or demoted administrator.
	var role string
	if err=tx.QueryRowContext(ctx,"SELECT role FROM users WHERE id=?",sender).Scan(&role);err!=nil || !auth.HasPermission(role,auth.PermissionAdmin) {
		_,_=tx.ExecContext(ctx,"UPDATE burris_v2_jobs SET status='failed',note='Sender no longer authorized' WHERE id=?",id);_=tx.Commit();return
	}
	if repeat==1 || repeat==7 || repeat==30 {
		step:=int64(repeat)*86400
		next:=runAt+step
		if next<=now {next+=((now-next)/step+1)*step}
		// Unique key derived from occurrence and bounded in length.
		a2:=a;a2.Key=fmt.Sprintf("repeat-%d-%d-%d",sender,next,now)
		// Include original rowid to distinguish multiple series in one second.
		var rowid int64
		if err=tx.QueryRowContext(ctx,"SELECT rowid FROM burris_v2_jobs WHERE id=?",id).Scan(&rowid);err!=nil{return}
		a2.Key=fmt.Sprintf("%s-%d",a2.Key,rowid);a2.RunAt=next
		data,_:=json.Marshal(a2)
		if _,err=tx.ExecContext(ctx,"INSERT INTO burris_v2_jobs(id,sender,payload,status,run_at,repeat_days,created) VALUES(?,?,?,'queued',?,?,?)",a2.Key,sender,string(data),next,repeat,now);err!=nil{return}
	}
	if err=tx.Commit();err!=nil{return}
	h.v2Deliver(ctx,id,sender,a)
}
func v2Target(a announcementV2Request,sender int64,u announcementCandidate) bool {
	switch a.Audience {
	case "all":return true
	case "self":return u.id==sender
	case "admins":return auth.HasPermission(u.role,auth.PermissionAdmin)
	case "selected":for _,id:=range a.UserIDs{if id==u.id{return true}}
	}
	return false
}

func (h *Handler) v2Deliver(ctx context.Context,id string,sender int64,a announcementV2Request) {
	status,note:="failed","Announcement not sent"
	recipients,skipped,sent,failed:=0,0,0,0
	defer func(){
		save,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel()
		if _,err:=h.db.ExecContext(save,"UPDATE burris_v2_jobs SET status=?,note=?,recipients=?,skipped=?,sent=?,failed=? WHERE id=? AND status='running'",status,note,recipients,skipped,sent,failed,id);err!=nil {h.logger.Error("announcement outcome","err",err)}
	}()
	users,err:=h.v2Candidates(ctx);if err!=nil {note="Recipient lookup failed";return}
	p,err:=h.prefs.Policy();if err!=nil {note="Notification settings unavailable";return}
	var targets,pushIDs []int64
	for _,u:=range users{
		if !v2Target(a,sender,u){continue}
		targets=append(targets,u.id)
		pref,e:=h.prefs.Get(u.id);if e!=nil {note="Recipient preferences unavailable";return}
		if p.Enabled && pref.PushEnabled && u.tokens>0 {pushIDs=append(pushIDs,u.id)} else {skipped++}
	}
	if len(targets)==0 || len(targets)>500 {note="No recipients or more than 500 recipients";return}
	// Inbox is available to all addressed users, including those who chose
	// not to receive a phone alert. It is not a substitute for push consent.
	tx,err:=h.db.BeginTx(ctx,nil);if err!=nil {note="Inbox storage unavailable";return};defer tx.Rollback()
	for _,userID:=range targets {
		if _,err=tx.ExecContext(ctx,"INSERT INTO burris_v2_inbox(job_id,user_id,title,body,created) VALUES(?,?,?,?,?)",id,userID,a.Title,a.Body,time.Now().Unix());err!=nil {note="Inbox storage failed";return}
	}
	if err=tx.Commit();err!=nil{note="Inbox storage failed";return}
	recipients=len(targets)
	status,note="inbox_only","Saved to inbox; no push-eligible devices"
	if len(pushIDs)==0{return}
	if h.mgr==nil {note="Saved to inbox; push gateway disabled";return}
	sendCtx,cancel:=context.WithTimeout(ctx,30*time.Second);defer cancel()
	client:=h.mgr.Ensure(sendCtx)
	if client==nil {note="Saved to inbox; push gateway unavailable";return}
	// Recheck master policy immediately before sending.
	eligible:=[]int64{}
	for _,uid:=range pushIDs {
		e:=h.prefs.checkMasters(uid)
		if e==nil {eligible=append(eligible,uid)} else if errors.Is(e,errPushDisabledForUser)||errors.Is(e,errPushDisabledByServer) {skipped++} else {note="Saved to inbox; push preferences unavailable";return}
	}
	if len(eligible)==0{return}
	status,note="unknown","Gateway outcome unknown; do not automatically retry"
	resp,err:=client.Send(sendCtx,eligible,a.Title,a.Body,map[string]any{"type":"announcement"})
	if err!=nil {h.reportIfUnauthorized(client,err);h.logger.Error("announcement gateway","err",err);return}
	sent,failed=resp.Sent,resp.Failed
	status,note="completed","Gateway acceptance is not proof of display"
	pruneDeadTokens(h.db,h.logger,resp)
}
