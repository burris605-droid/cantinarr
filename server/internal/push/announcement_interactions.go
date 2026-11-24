package push

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

func (h *Handler) V2InteractionPolicy(w http.ResponseWriter,r *http.Request) {
	if !h.inboxAdmin(w,r){return}
	var a struct {Replies *bool `json:"replies"`; Acknowledgments *bool `json:"acknowledgments"`}
	if r.Method==http.MethodPut{
		d:=json.NewDecoder(http.MaxBytesReader(w,r.Body,4096));d.DisallowUnknownFields()
		if d.Decode(&a)!=nil||d.Decode(new(any))!=io.EOF||a.Replies==nil||a.Acknowledgments==nil{writeJSON(w,400,map[string]string{"error":"invalid policy"});return}
		if _,err:=h.db.ExecContext(r.Context(),"UPDATE burris_v2_interaction_policy SET replies=?,acknowledgments=? WHERE id=1",*a.Replies,*a.Acknowledgments);err!=nil{writeJSON(w,500,map[string]string{"error":"policy update failed"});return}
	}
	var replies,acks bool
	if err:=h.db.QueryRowContext(r.Context(),"SELECT replies,acknowledgments FROM burris_v2_interaction_policy WHERE id=1").Scan(&replies,&acks);err!=nil{writeJSON(w,500,map[string]string{"error":"policy unavailable"});return}
	writeJSON(w,200,map[string]bool{"replies":replies,"acknowledgments":acks})
}

func (h *Handler) V2Unread(w http.ResponseWriter,r *http.Request) {
	c:=auth.GetClaims(r.Context())
	if c==nil{writeJSON(w,401,map[string]string{"error":"unauthorized"});return}
	if !h.v2Ready(w,r){return}
	var count int
	if err:=h.db.QueryRowContext(r.Context(),"SELECT COUNT(*) FROM burris_v2_inbox i LEFT JOIN burris_v2_inbox_meta m ON m.job_id=i.job_id WHERE i.user_id=? AND i.read_at=0 AND (COALESCE(m.expires,0)=0 OR m.expires>?)",c.UserID,time.Now().Unix()).Scan(&count);err!=nil{writeJSON(w,500,map[string]string{"error":"count unavailable"});return}
	writeJSON(w,200,map[string]int{"unread":count})
}

func (h *Handler) V2Acknowledge(w http.ResponseWriter,r *http.Request) {
	c:=auth.GetClaims(r.Context())
	if c==nil{writeJSON(w,401,map[string]string{"error":"unauthorized"});return}
	if !h.v2Ready(w,r){return}
	id,err:=strconv.ParseInt(chi.URLParam(r,"id"),10,64)
	if err!=nil||id<=0{writeJSON(w,400,map[string]string{"error":"invalid message"});return}
	tx,err:=h.db.BeginTx(r.Context(),nil)
	if err!=nil{writeJSON(w,500,map[string]string{"error":"acknowledgment failed"});return};defer tx.Rollback()
	now:=time.Now().Unix()
	var allowed bool
	err=tx.QueryRowContext(r.Context(),"SELECT EXISTS(SELECT 1 FROM burris_v2_inbox i JOIN burris_v2_interaction_meta x ON x.job_id=i.job_id LEFT JOIN burris_v2_inbox_meta m ON m.job_id=i.job_id WHERE i.id=? AND i.user_id=? AND x.require_ack=1 AND (COALESCE(m.expires,0)=0 OR m.expires>?) AND (SELECT acknowledgments FROM burris_v2_interaction_policy WHERE id=1)=1)",id,c.UserID,now).Scan(&allowed)
	if err!=nil{writeJSON(w,500,map[string]string{"error":"acknowledgment failed"});return}
	if !allowed{writeJSON(w,404,map[string]string{"error":"message unavailable or acknowledgments disabled"});return}
	_,err=tx.ExecContext(r.Context(),"INSERT OR IGNORE INTO burris_v2_ack(inbox_id,acknowledged) VALUES(?,?)",id,now)
	if err==nil{_,err=tx.ExecContext(r.Context(),"UPDATE burris_v2_inbox SET read_at=CASE WHEN read_at=0 THEN ? ELSE read_at END WHERE id=? AND user_id=?",now,id,c.UserID)}
	if err==nil{err=tx.Commit()}
	if err!=nil{writeJSON(w,500,map[string]string{"error":"acknowledgment failed"});return}
	writeJSON(w,200,map[string]bool{"success":true})
}

func (h *Handler) V2Replies(w http.ResponseWriter,r *http.Request){h.v2Thread(w,r,false)}
func (h *Handler) V2AdminReplies(w http.ResponseWriter,r *http.Request){h.v2Thread(w,r,true)}

// Thread ownership is checked in the SQL itself. Each recipient has a
// separate conversation, and only administrators can reply as the server.
func (h *Handler) v2Thread(w http.ResponseWriter,r *http.Request,admin bool){
	c:=auth.GetClaims(r.Context())
	if c==nil||admin&&!auth.HasPermission(c.Role,auth.PermissionAdmin){writeJSON(w,403,map[string]string{"error":"forbidden"});return}
	if !h.v2Ready(w,r){return}
	id,err:=strconv.ParseInt(chi.URLParam(r,"id"),10,64)
	if err!=nil||id<=0{writeJSON(w,400,map[string]string{"error":"invalid message"});return}
	if r.Method==http.MethodPost{
		var a struct{Body string `json:"body"`}
		d:=json.NewDecoder(http.MaxBytesReader(w,r.Body,8192));d.DisallowUnknownFields()
		if d.Decode(&a)!=nil||d.Decode(new(any))!=io.EOF{writeJSON(w,400,map[string]string{"error":"invalid reply"});return}
		a.Body=strings.TrimSpace(a.Body)
		if a.Body==""||len(a.Body)>2000{writeJSON(w,400,map[string]string{"error":"reply must be 1–2000 bytes"});return}
		now:=time.Now().Unix()
		res,err:=h.db.ExecContext(r.Context(),"INSERT INTO burris_v2_replies(inbox_id,from_admin,body,created) SELECT i.id,?,?,? FROM burris_v2_inbox i LEFT JOIN burris_v2_inbox_meta m ON m.job_id=i.job_id WHERE i.id=? AND (? OR i.user_id=?) AND (COALESCE(m.expires,0)=0 OR m.expires>?) AND (SELECT replies FROM burris_v2_interaction_policy WHERE id=1)=1 AND (SELECT COUNT(*) FROM burris_v2_replies q WHERE q.inbox_id=i.id)<200",admin,a.Body,now,id,admin,c.UserID,now)
		if err!=nil{writeJSON(w,500,map[string]string{"error":"reply failed"});return}
		n,_:=res.RowsAffected();if n!=1{writeJSON(w,409,map[string]string{"error":"thread unavailable, full, or replies disabled"});return}
		if admin {_,_=h.db.ExecContext(r.Context(),"UPDATE burris_v2_inbox SET read_at=0 WHERE id=?",id)}
		writeJSON(w,200,map[string]bool{"success":true});return
	}
	rows,err:=h.db.QueryContext(r.Context(),"SELECT q.id,q.from_admin,q.body,q.created FROM burris_v2_replies q JOIN burris_v2_inbox i ON i.id=q.inbox_id LEFT JOIN burris_v2_inbox_meta m ON m.job_id=i.job_id WHERE i.id=? AND (? OR i.user_id=?) AND (COALESCE(m.expires,0)=0 OR m.expires>?) ORDER BY q.id LIMIT 200",id,admin,c.UserID,time.Now().Unix())
	if err!=nil{writeJSON(w,500,map[string]string{"error":"thread unavailable"});return}
	defer rows.Close()
	out:=[]map[string]any{}
	for rows.Next(){var rid,created int64;var fromAdmin bool;var body string;if err=rows.Scan(&rid,&fromAdmin,&body,&created);err!=nil{break};out=append(out,map[string]any{"id":rid,"from_admin":fromAdmin,"body":body,"created":created})}
	if err!=nil||rows.Err()!=nil{writeJSON(w,500,map[string]string{"error":"thread unavailable"});return}
	writeJSON(w,200,out)
}
