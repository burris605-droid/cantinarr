package push

import "testing"

func TestAnnouncementValidation(t *testing.T) {
 cases:=[]struct{name string; req announcementRequest; valid bool}{
  {"self",announcementRequest{Audience:"self",Title:" Test ",Body:" Hello "},true},
  {"all",announcementRequest{Audience:"all",Title:"Update",Body:"Maintenance"},true},
  {"selected",announcementRequest{Audience:"selected",Title:"Update",Body:"Maintenance",UserIDs:[]int64{1,2}},true},
  {"selected empty",announcementRequest{Audience:"selected",Title:"Update",Body:"Maintenance"},false},
  {"negative id",announcementRequest{Audience:"selected",Title:"Update",Body:"Maintenance",UserIDs:[]int64{-1}},false},
  {"ambiguous all",announcementRequest{Audience:"all",Title:"Update",Body:"Maintenance",UserIDs:[]int64{1}},false},
  {"blank",announcementRequest{Audience:"self",Title:" ",Body:"Maintenance"},false},
  {"unknown",announcementRequest{Audience:"other",Title:"Update",Body:"Maintenance"},false},
 }
 for _,c:=range cases {
  t.Run(c.name,func(t *testing.T){ if got:=c.req.valid();got!=c.valid {t.Fatalf("valid = %v, want %v",got,c.valid)} })
 }
}
