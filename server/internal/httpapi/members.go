package httpapi

import (
	"net/http"
	"strings"

	"niudbm/server/internal/store"
)

type memberReq struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	items, err := s.st.ListInstanceMembers(r.Context(), in.ID)
	if err != nil {
		s.fail(w, r, errInternal("读取成员失败"))
		return
	}
	writeOK(w, r, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleUpsertMember(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireInstanceAdmin(w, r)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	var req memberReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	userID := strings.TrimSpace(r.PathValue("uid"))
	if userID == "" {
		s.fail(w, r, errBadRequest("缺少飞牛用户 ID"))
		return
	}
	role := strings.TrimSpace(req.Role)
	if role != store.MemberRoleViewer && role != store.MemberRoleOperator && role != store.MemberRoleAdmin {
		s.fail(w, r, errBadRequest("成员角色只能是 viewer / operator / admin"))
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = userID
	}
	member := store.InstanceMember{
		InstanceID: in.ID, UserID: userID, Username: username, Role: role,
	}
	if err := s.st.UpsertInstanceMember(r.Context(), member); err != nil {
		s.fail(w, r, errInternal("保存成员失败"))
		return
	}
	s.audit(r, id, "instance.member.update", "instance", in.ID, "ok", username+" → "+role)
	writeOK(w, r, member)
}

func (s *Server) handleDeleteMember(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireInstanceAdmin(w, r)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	userID := strings.TrimSpace(r.PathValue("uid"))
	if err := s.st.DeleteInstanceMember(r.Context(), in.ID, userID); err != nil {
		s.fail(w, r, errNotFound("成员不存在"))
		return
	}
	s.audit(r, id, "instance.member.delete", "instance", in.ID, "ok", userID)
	writeOK(w, r, map[string]any{"userId": userID})
}
