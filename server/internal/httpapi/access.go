package httpapi

import (
	"context"
	"net/http"
	"strings"

	"niudbm/server/internal/store"
)

func (s *Server) visibleInstances(ctx context.Context, id identity, list []store.Instance) ([]store.Instance, map[string]string, error) {
	if id.IsAdmin {
		return list, nil, nil
	}
	roles, err := s.st.InstanceIDsForMember(ctx, id.UserID)
	if err != nil {
		return nil, nil, err
	}
	out := make([]store.Instance, 0, len(list))
	for _, in := range list {
		if _, ok := roles[in.ID]; ok {
			out = append(out, in)
		}
	}
	return out, roles, nil
}

func roleRank(role string) int {
	switch role {
	case store.MemberRoleAdmin:
		return 3
	case store.MemberRoleOperator:
		return 2
	case store.MemberRoleViewer:
		return 1
	default:
		return 0
	}
}

func instanceIDFromPattern(pattern string) string {
	if !strings.Contains(pattern, "/instances/{id}") {
		return ""
	}
	return "{id}"
}

func (s *Server) requestInstanceRole(r *http.Request, id identity) (string, error) {
	if id.IsAdmin {
		return store.MemberRoleAdmin, nil
	}
	if instanceIDFromPattern(r.Pattern) == "" {
		return "", store.ErrNotFound
	}
	instanceID := r.PathValue("id")
	if instanceID == "" {
		return "", store.ErrNotFound
	}
	return s.st.InstanceMemberRole(r.Context(), instanceID, id.UserID)
}

func (s *Server) canReadRequest(r *http.Request, id identity) bool {
	if id.IsAdmin {
		return true
	}
	if instanceIDFromPattern(r.Pattern) != "" {
		role, err := s.requestInstanceRole(r, id)
		return err == nil && roleRank(role) >= roleRank(store.MemberRoleViewer)
	}
	switch {
	case r.Pattern == "GET /api/v1/instances":
		return true
	case r.Pattern == "GET /api/v1/overview":
		return true
	case strings.HasPrefix(r.Pattern, "GET /api/v1/jobs"):
		return true
	case r.Pattern == "POST /api/v1/jobs/{id}/cancel":
		return true
	case strings.HasPrefix(r.Pattern, "GET /api/v1/backups"):
		return true
	default:
		return false
	}
}

func (s *Server) canWriteRequest(r *http.Request, id identity, instanceAdmin bool) bool {
	if id.IsAdmin {
		return true
	}
	if instanceIDFromPattern(r.Pattern) == "" {
		return false
	}
	role, err := s.requestInstanceRole(r, id)
	if err != nil {
		return false
	}
	min := store.MemberRoleOperator
	if instanceAdmin {
		min = store.MemberRoleAdmin
	}
	return roleRank(role) >= roleRank(min)
}

func (s *Server) requireInstanceAdmin(w http.ResponseWriter, r *http.Request) (identity, bool) {
	id, ok := s.sessionUserOf(r)
	if !ok {
		writeErr(w, r, errUnauthorized("请先登录后再操作"))
		return identity{}, false
	}
	if !s.canWriteRequest(r, id, true) {
		writeErr(w, r, apiErr{http.StatusForbidden, codeForbidden, "只有系统管理员或该服务管理员可以执行此操作"})
		return identity{}, false
	}
	return id, true
}

func (s *Server) canOperateInstance(r *http.Request, id identity, instanceID string) bool {
	if id.IsAdmin {
		return true
	}
	role, err := s.st.InstanceMemberRole(r.Context(), instanceID, id.UserID)
	return err == nil && roleRank(role) >= roleRank(store.MemberRoleOperator)
}

func (s *Server) applyAccess(r *http.Request, id identity, view *instanceView) {
	if id.IsAdmin {
		view.AccessRole = store.MemberRoleAdmin
		return
	}
	if role, err := s.st.InstanceMemberRole(r.Context(), view.ID, id.UserID); err == nil {
		view.AccessRole = role
	}
}
