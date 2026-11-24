package push

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

func (h *Handler) inboxAdmin(w http.ResponseWriter,r *http.Request) bool {
	c:=auth.GetClaims(r.Context())
	if c==nil || !auth.HasPermission(c.Role,auth.PermissionAdmin) {writeJSON(w,403,map[string]string{"error":"administrator required"});return false}
	return h.v2Ready(w,r)
}

// Lists inbox copies rather than delivery history, so deleting history never
// removes the administrator's ability to manage a published announcement.
func (h *Handler) V2InboxAdmin(w http.ResponseWriter,r *http.Request) {
	if !h.inboxAdmin(w,r){return}
	rows,err:=h.db.QueryContext(r.Context(),"SELECT i.job_id, i.title, i.body, MIN(i.created), COUNT(*), SUM(CASE WHEN i.read_at>0 THEN 1 ELSE 0 END), COALESCE(m.pinned,0), COALESCE(m.expires,0) FROM burris_v2_inbox i LEFT JOIN burris_v2_inbox_meta m ON m.job_id=i.job_id GROUP BY i.job_id,i.title,i.body,m.pinned,m.expires ORDER BY COALESCE(m.pinned,0) DESC, MIN(i.created) DESC LIMIT 500")
	if err!=nil {writeJSON(w,500,map[string]string{"error":"inbox list unavailable"});return}
	defer rows.Close()
	out:=[]map[string]any{}
	for rows.Next(){
		var id,title,body string;var created,count,read,expires int64;var pinned bool
		if err=rows.Scan(&id,&title,&body,&created,&count,&read,&pinned,&expires);err!=nil{break}
		out=append(out,map[string]any{"id":id,"title":title,"body":body,"created":created,"count":count,"read":read,"pinned":pinned,"expires":expires})
	}
	if err!=nil||rows.Err()!=nil {writeJSON(w,500,map[string]string{"error":"inbox list unavailable"});return}
	writeJSON(w,200,out)
}

func (h *Handler) V2InboxDetails(w http.ResponseWriter,r *http.Request) {
	if !h.inboxAdmin(w,r){return}
	id:=chi.URLParam(r,"id")
	rows,err:=h.db.QueryContext(r.Context(),"SELECT i.user_id,u.username,i.read_at FROM burris_v2_inbox i JOIN users u ON u.id=i.user_id WHERE i.job_id=? ORDER BY u.username",id)
	if err!=nil {writeJSON(w,500,map[string]string{"error":"read statistics unavailable"});return}
	defer rows.Close()
	out:=[]map[string]any{}
	for rows.Next(){var uid,read int64;var username string;if err=rows.Scan(&uid,&username,&read);err!=nil{break};out=append(out,map[string]any{"user_id":uid,"username":username,"read_at":read})}
	if err!=nil||rows.Err()!=nil {writeJSON(w,500,map[string]string{"error":"read statistics unavailable"});return}
	writeJSON(w,200,out)
}
type inboxEdit struct {
	Title string `json:"title"`
	Body string `json:"body"`
	Pinned bool `json:"pinned"`
	Expires int64 `json:"expires"`
	ResetRead bool `json:"reset_read"`
}

func (h *Handler) V2InboxEdit(w http.ResponseWriter,r *http.Request) {
	if !h.inboxAdmin(w,r){return}
	id:=chi.URLParam(r,"id")
	var a inboxEdit
	dec:=json.NewDecoder(http.MaxBytesReader(w,r.Body,8192));dec.DisallowUnknownFields()
	now:=time.Now().Unix()
	if dec.Decode(&a)!=nil||dec.Decode(new(any))!=io.EOF||a.Expires<0||a.Expires>now+366*86400 {writeJSON(w,400,map[string]string{"error":"invalid inbox settings"});return}
	v:=announcementRequest{Audience:"self",Title:a.Title,Body:a.Body}
	if !v.valid(){writeJSON(w,400,map[string]string{"error":"invalid message"});return}
	tx,err:=h.db.BeginTx(r.Context(),nil)
	if err!=nil{writeJSON(w,500,map[string]string{"error":"edit failed"});return};defer tx.Rollback()
	res,err:=tx.ExecContext(r.Context(),"UPDATE burris_v2_inbox SET title=?,body=?,read_at=CASE WHEN ? THEN 0 ELSE read_at END WHERE job_id=?",v.Title,v.Body,a.ResetRead,id)
	if err==nil {n,_:=res.RowsAffected();if n==0{writeJSON(w,404,map[string]string{"error":"message not found"});return}}
	if err==nil {_,err=tx.ExecContext(r.Context(),"INSERT INTO burris_v2_inbox_meta(job_id,pinned,expires) VALUES(?,?,?) ON CONFLICT(job_id) DO UPDATE SET pinned=excluded.pinned,expires=excluded.expires",id,a.Pinned,a.Expires)}
	if err==nil {err=tx.Commit()}
	if err!=nil {writeJSON(w,500,map[string]string{"error":"edit failed"});return}
	writeJSON(w,200,map[string]bool{"success":true})
}

func (h *Handler) V2InboxRecall(w http.ResponseWriter,r *http.Request) {
	if !h.inboxAdmin(w,r){return}
	id:=chi.URLParam(r,"id")
	tx,err:=h.db.BeginTx(r.Context(),nil)
	if err!=nil {writeJSON(w,500,map[string]string{"error":"recall failed"});return};defer tx.Rollback()
	res,err:=tx.ExecContext(r.Context(),"DELETE FROM burris_v2_inbox WHERE job_id=?",id)
	if err==nil {_,err=tx.ExecContext(r.Context(),"DELETE FROM burris_v2_inbox_meta WHERE job_id=?",id)}
	if err==nil {err=tx.Commit()}
	if err!=nil {writeJSON(w,500,map[string]string{"error":"recall failed"});return}
	n,_:=res.RowsAffected()
	writeJSON(w,200,map[string]any{"removed":n})
}

// Bulk clearing is explicit: only the inbox, never schedules or history.
func (h *Handler) V2InboxClear(w http.ResponseWriter,r *http.Request) {
	if !h.inboxAdmin(w,r){return}
	var a struct{UserID int64 `json:"user_id"`; All bool `json:"all"`; OlderDays int `json:"older_days"`; IncludePinned bool `json:"include_pinned"`}
	dec:=json.NewDecoder(http.MaxBytesReader(w,r.Body,8192));dec.DisallowUnknownFields()
	if dec.Decode(&a)!=nil||dec.Decode(new(any))!=io.EOF||a.UserID<0||(a.All==(a.UserID>0))||(a.OlderDays!=0&&a.OlderDays!=30&&a.OlderDays!=90){writeJSON(w,400,map[string]string{"error":"choose a valid scope"});return}
	cutoff:=time.Now().Unix()-int64(a.OlderDays)*86400
	res,err:=h.db.ExecContext(r.Context(),"DELETE FROM burris_v2_inbox WHERE (? OR user_id=?) AND created<=? AND (? OR NOT EXISTS(SELECT 1 FROM burris_v2_inbox_meta m WHERE m.job_id=burris_v2_inbox.job_id AND m.pinned=1))",a.All,a.UserID,cutoff,a.IncludePinned)
	if err!=nil{writeJSON(w,500,map[string]string{"error":"clear failed"});return}
	n,_:=res.RowsAffected()
	writeJSON(w,200,map[string]any{"removed":n})
}
func (h *Handler) V2InboxPolicy(w http.ResponseWriter,r *http.Request) {
	if !h.inboxAdmin(w,r){return}
	var a struct{AllowDelete *bool `json:"allow_delete"`}
	if r.Method==http.MethodPut{
		dec:=json.NewDecoder(http.MaxBytesReader(w,r.Body,8192));dec.DisallowUnknownFields()
		if dec.Decode(&a)!=nil||dec.Decode(new(any))!=io.EOF||a.AllowDelete==nil {writeJSON(w,400,map[string]string{"error":"invalid policy"});return}
		if _,err:=h.db.ExecContext(r.Context(),"UPDATE burris_v2_inbox_policy SET allow_delete=? WHERE id=1",*a.AllowDelete);err!=nil{writeJSON(w,500,map[string]string{"error":"policy save failed"});return}
	}
	var enabled bool
	if err:=h.db.QueryRowContext(r.Context(),"SELECT allow_delete FROM burris_v2_inbox_policy WHERE id=1").Scan(&enabled);err!=nil{writeJSON(w,500,map[string]string{"error":"policy unavailable"});return}
	writeJSON(w,200,map[string]bool{"allow_delete":enabled})
}

func (h *Handler) V2InboxDeleteCopy(w http.ResponseWriter,r *http.Request) {
	if !h.inboxAdmin(w,r){return}
	uid,err:=strconv.ParseInt(chi.URLParam(r,"userID"),10,64)
	if err!=nil||uid<=0{writeJSON(w,400,map[string]string{"error":"invalid user"});return}
	res,err:=h.db.ExecContext(r.Context(),"DELETE FROM burris_v2_inbox WHERE job_id=? AND user_id=?",chi.URLParam(r,"id"),uid)
	if err!=nil{writeJSON(w,500,map[string]string{"error":"delete failed"});return}
	n,_:=res.RowsAffected();writeJSON(w,200,map[string]any{"removed":n})
}
