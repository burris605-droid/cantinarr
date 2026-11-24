package push

import (
 "context"
 "encoding/json"
 "errors"
 "io"
 "net/http"
 "strings"
 "time"

 "github.com/windoze95/cantinarr-server/internal/auth"
)

type announcementRequest struct {
 Audience string `json:"audience"`
 UserIDs []int64 `json:"user_ids"`
 Title string `json:"title"`
 Body string `json:"body"`
}

func (a *announcementRequest) valid() bool {
 a.Title, a.Body = strings.TrimSpace(a.Title), strings.TrimSpace(a.Body)
 if a.Title == "" || a.Body == "" || len(a.Title)>120 || len(a.Body)>1000 || len(a.UserIDs)>500 { return false }
 switch a.Audience {
 case "all", "self": return len(a.UserIDs)==0
 case "selected":
  if len(a.UserIDs)==0 { return false }
  for _, id := range a.UserIDs { if id<=0 { return false } }
  return true
 }
 return false
}

type announcementRecord struct {
 ID int64 `json:"id"`
 Created string `json:"created"`
 Audience string `json:"audience"`
 Title string `json:"title"`
 Body string `json:"body"`
 Status string `json:"status"`
 Recipients int `json:"recipients"`
 Skipped int `json:"skipped"`
 Sent int `json:"sent"`
 Failed int `json:"failed"`
}

func (h *Handler) announcementTable(ctx context.Context) error {
 _, err := h.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS burris_announcements (
 id INTEGER PRIMARY KEY, created TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
 sender_id INTEGER NOT NULL, audience TEXT NOT NULL, title TEXT NOT NULL,
 body TEXT NOT NULL, status TEXT NOT NULL, recipients INTEGER NOT NULL,
 skipped INTEGER NOT NULL, sent INTEGER NOT NULL DEFAULT 0, failed INTEGER NOT NULL DEFAULT 0)`)
 return err
}

// AnnouncementHistory is protected by the router's administrator permission.
func (h *Handler) AnnouncementHistory(w http.ResponseWriter, r *http.Request) {
 if auth.GetClaims(r.Context())==nil { writeJSON(w,401,map[string]string{"error":"unauthorized"}); return }
 if err:=h.announcementTable(r.Context()); err!=nil { writeJSON(w,500,map[string]string{"error":"history unavailable"}); return }
 rows,err:=h.db.QueryContext(r.Context(),`SELECT id,created,audience,title,body,status,recipients,skipped,sent,failed FROM burris_announcements ORDER BY id DESC LIMIT 50`)
 if err!=nil { writeJSON(w,500,map[string]string{"error":"history unavailable"}); return }
 defer rows.Close()
 out:=[]announcementRecord{}
 for rows.Next() {
  var a announcementRecord
  if err:=rows.Scan(&a.ID,&a.Created,&a.Audience,&a.Title,&a.Body,&a.Status,&a.Recipients,&a.Skipped,&a.Sent,&a.Failed);err!=nil { writeJSON(w,500,map[string]string{"error":"history unavailable"}); return }
  out=append(out,a)
 }
 if rows.Err()!=nil { writeJSON(w,500,map[string]string{"error":"history unavailable"}); return }
 writeJSON(w,200,out)
}

// SendAnnouncement uses the existing tenant-scoped push gateway. A user who
// disabled push, or has no active registered device, is never targeted.
func (h *Handler) SendAnnouncement(w http.ResponseWriter,r *http.Request) {
 claims:=auth.GetClaims(r.Context())
 if claims==nil { writeJSON(w,401,map[string]string{"error":"unauthorized"}); return }
 var a announcementRequest
 dec:=json.NewDecoder(http.MaxBytesReader(w,r.Body,8192))
 dec.DisallowUnknownFields()
 if dec.Decode(&a)!=nil || dec.Decode(new(any))!=io.EOF || !a.valid() { writeJSON(w,400,map[string]string{"error":"invalid announcement"}); return }
 ctx:=r.Context()
 p,err:=h.prefs.Policy()
 if err!=nil { writeJSON(w,500,map[string]string{"error":"notification settings unavailable"}); return }
 if !p.Enabled { writeTestPolicyError(w,errPushDisabledByServer); return }
 // Collect rows before checking preferences (SQLite can use a single connection).
 rows,err:=h.db.QueryContext(ctx,`SELECT u.id, EXISTS(SELECT 1 FROM push_tokens p JOIN devices d ON d.id=p.device_id AND d.user_id=u.id WHERE p.user_id=u.id AND d.revoked_at IS NULL) FROM users u ORDER BY u.id`)
 if err!=nil { writeJSON(w,500,map[string]string{"error":"users unavailable"}); return }
 type candidate struct {id int64; active bool}
 var users []candidate
 for rows.Next() { var c candidate; if err=rows.Scan(&c.id,&c.active);err!=nil { break }; users=append(users,c) }
 if err==nil { err=rows.Err() }; rows.Close()
 if err!=nil { writeJSON(w,500,map[string]string{"error":"users unavailable"}); return }
 selected:=map[int64]bool{}
 for _,id:=range a.UserIDs {selected[id]=true}
 ids:=[]int64{}
 skipped:=0
 for _,u:=range users {
  if a.Audience=="self" && u.id!=claims.UserID || a.Audience=="selected" && !selected[u.id] {continue}
  if !u.active {skipped++;continue}
  if err:=h.prefs.checkMasters(u.id);err!=nil {
   if errors.Is(err,errPushDisabledForUser) {skipped++;continue}
   writeTestPolicyError(w,err);return
  }
  ids=append(ids,u.id)
 }
 if len(ids)==0 {writeJSON(w,409,map[string]string{"error":"no eligible users with registered push devices"});return}
 if len(ids)>500 {writeJSON(w,400,map[string]string{"error":"select at most 500 eligible users"});return}
 if h.mgr==nil {writeJSON(w,503,map[string]string{"error":"push not configured"});return}
 sendCtx,cancel:=context.WithTimeout(ctx,30*time.Second);defer cancel()
 client:=h.mgr.Ensure(sendCtx)
 if client==nil {writeJSON(w,503,map[string]string{"error":"push not configured"});return}
 if err=h.announcementTable(ctx);err!=nil {writeJSON(w,500,map[string]string{"error":"history unavailable"});return}
 row,err:=h.db.ExecContext(ctx,`INSERT INTO burris_announcements(sender_id,audience,title,body,status,recipients,skipped) VALUES(?,?,?,?,?,?,?)`,claims.UserID,a.Audience,a.Title,a.Body,"pending",len(ids),skipped)
 if err!=nil {writeJSON(w,500,map[string]string{"error":"history unavailable"});return}
 id,err:=row.LastInsertId()
 if err!=nil {writeJSON(w,500,map[string]string{"error":"history unavailable"});return}
 out:=announcementRecord{ID:id,Audience:a.Audience,Title:a.Title,Body:a.Body,Recipients:len(ids),Skipped:skipped,Status:"unknown"}
 resp,err:=client.Send(sendCtx,ids,a.Title,a.Body,map[string]any{"type":"announcement"})
 if err!=nil {h.reportIfUnauthorized(client,err);h.logger.Error("announcement gateway error","err",err)} else {
  out.Status="completed";out.Sent=resp.Sent;out.Failed=resp.Failed
  pruneDeadTokens(h.db,h.logger,resp)
 }
 // Record the outcome even when the browser disconnected. Never auto-retry
 // ambiguous delivery failures: a gateway may have already accepted the push.
 recordCtx,done:=context.WithTimeout(context.Background(),5*time.Second);defer done()
 if _,e:=h.db.ExecContext(recordCtx,`UPDATE burris_announcements SET status=?,sent=?,failed=? WHERE id=?`,out.Status,out.Sent,out.Failed,id);e!=nil {h.logger.Error("announcement history update","err",e)}
 if err!=nil {writeJSON(w,502,out);return}
 writeJSON(w,200,out)
}
