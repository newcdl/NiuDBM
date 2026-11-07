package httpapi

import (
	"net/http"
	"strings"

	"niudbm/server/internal/mongoctl"
	"niudbm/server/internal/mysqlctl"
	"niudbm/server/internal/pgctl"
	"niudbm/server/internal/store"
)

// roleGrantReq 是一条库授权：账号在这个库上拿到什么档位。
type roleGrantReq struct {
	Database string `json:"database"`
	Level    string `json:"level"`
}

type createUserReq struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	// Hosts 是允许来源：空或 ["%"] 表示所有人，具体 IP 表示只允许这些地址
	Hosts []string `json:"hosts"`
	// Grants 是一次授好的多个库：一个账号可能既要读业务库、又要连报表库
	Grants []roleGrantReq `json:"grants"`
	// Database / Level 是只授一个库时的简写，兼容旧表单
	Database string `json:"database"`
	Level    string `json:"level"`
}

type updateUserReq struct {
	Password string `json:"password"`
	// Hosts 用指针区分"没传"（不改来源）与"传了"（以这份为准）
	Hosts *[]string `json:"hosts"`
	// Grants 用指针区分"没传"（不改授权）与"传了空数组"（撤销全部授权）
	Grants   *[]roleGrantReq `json:"grants"`
	Database string          `json:"database"`
	Level    string          `json:"level"`
}

// roleItem 是用户展示结构：账号、能否登录、是否超级用户、库授权列表。
type roleItem struct {
	Name   string               `json:"name"`
	Login  bool                 `json:"login"`
	Super  bool                 `json:"super"`
	Level  string               `json:"level"`
	System bool                 `json:"system"`
	Grants []mysqlctl.RoleGrant `json:"grants"`
	// Hosts 是允许来源，界面显示成 user@% 或 user@ip
	Hosts []string `json:"hosts"`
}

// grantsOf 把请求里的授权字段归一化：新表单用 grants，老表单用 database + level。
func grantsOf(grants *[]roleGrantReq, database, level string) []mysqlctl.RoleGrant {
	src := []roleGrantReq{}
	if grants != nil {
		src = *grants
	} else if strings.TrimSpace(database) != "" {
		src = []roleGrantReq{{Database: database, Level: level}}
	}
	out := make([]mysqlctl.RoleGrant, 0, len(src))
	for _, g := range src {
		name := strings.TrimSpace(g.Database)
		if name == "" {
			continue
		}
		lv := strings.TrimSpace(g.Level)
		if lv == "" {
			lv = mysqlctl.LevelRead
		}
		out = append(out, mysqlctl.RoleGrant{Database: name, Level: lv})
	}
	return out
}

// roleManager 抽象用户管理，处理器只依赖它（MySQL 与 PostgreSQL 各一套）。
type roleManager interface {
	Roles() ([]roleItem, error)
	CreateRole(name string, hosts []string, password string, grants []mysqlctl.RoleGrant) error
	UpdateRole(name string, hosts []string, password string, grants []mysqlctl.RoleGrant) error
	DropRole(name string) error
	ValidName(name string) error
	ValidLevel(level string) bool
	Levels() []string
	SystemUser() string
}

type mysqlRoles struct{ spec mysqlctl.Spec }

func (m mysqlRoles) Roles() ([]roleItem, error) {
	rs, err := m.spec.Roles()
	if err != nil {
		return nil, err
	}
	out := make([]roleItem, 0, len(rs))
	for _, r := range rs {
		out = append(out, roleItem{
			Name:   r.Name,
			Login:  r.Login,
			Super:  r.Super,
			Level:  r.Level,
			System: r.System,
			Grants: r.Grants,
			Hosts:  r.Hosts,
		})
	}
	return out, nil
}
func (m mysqlRoles) CreateRole(n string, h []string, p string, g []mysqlctl.RoleGrant) error {
	return m.spec.CreateRole(n, h, p, g)
}
func (m mysqlRoles) UpdateRole(n string, h []string, p string, g []mysqlctl.RoleGrant) error {
	return m.spec.UpdateRole(n, h, p, g)
}
func (m mysqlRoles) DropRole(n string) error  { return m.spec.DropRole(n) }
func (m mysqlRoles) ValidName(n string) error { return mysqlctl.ValidRoleName(n) }
func (m mysqlRoles) ValidLevel(l string) bool { return mysqlctl.ValidLevel(l) }
func (m mysqlRoles) Levels() []string {
	return []string{mysqlctl.LevelNone, mysqlctl.LevelRead, mysqlctl.LevelWrite, mysqlctl.LevelAll}
}
func (m mysqlRoles) SystemUser() string { return mysqlctl.SuperUser }

// pgRoles 是 PostgreSQL 的用户管理适配：档位语义与 MySQL 一致，
// 差别在来源（PG 没有 user@host）与"超级用户"的判定方式。
type pgRoles struct{ spec pgctl.Spec }

func (p pgRoles) Roles() ([]roleItem, error) {
	rs, err := p.spec.Roles()
	if err != nil {
		return nil, err
	}
	out := make([]roleItem, 0, len(rs))
	for _, r := range rs {
		out = append(out, roleItem{
			Name:   r.Name,
			Login:  r.Login,
			Super:  r.Super,
			Level:  r.Level,
			System: r.System,
			Grants: r.Grants,
			// PG 没有来源概念：留空，前端据此不展示 user@host
			Hosts: nil,
		})
	}
	return out, nil
}
func (p pgRoles) CreateRole(n string, h []string, pw string, g []mysqlctl.RoleGrant) error {
	return p.spec.CreateRole(n, h, pw, g)
}
func (p pgRoles) UpdateRole(n string, h []string, pw string, g []mysqlctl.RoleGrant) error {
	return p.spec.UpdateRole(n, h, pw, g)
}
func (p pgRoles) DropRole(n string) error  { return p.spec.DropRole(n) }
func (p pgRoles) ValidName(n string) error { return mysqlctl.ValidRoleName(n) }
func (p pgRoles) ValidLevel(l string) bool { return mysqlctl.ValidLevel(l) }
func (p pgRoles) Levels() []string {
	return []string{mysqlctl.LevelNone, mysqlctl.LevelRead, mysqlctl.LevelWrite, mysqlctl.LevelAll}
}
func (p pgRoles) SystemUser() string { return pgctl.RootUser }

type mongoRoles struct{ spec mongoctl.Spec }

func (m mongoRoles) Roles() ([]roleItem, error) {
	rs, err := m.spec.Roles()
	if err != nil {
		return nil, err
	}
	out := make([]roleItem, 0, len(rs))
	for _, r := range rs {
		out = append(out, roleItem{
			Name: r.Name, Login: r.Login, Super: r.Super, Level: r.Level,
			System: r.System, Grants: r.Grants, Hosts: nil,
		})
	}
	return out, nil
}

func (m mongoRoles) CreateRole(n string, h []string, p string, g []mysqlctl.RoleGrant) error {
	return m.spec.CreateRole(n, h, p, g)
}
func (m mongoRoles) UpdateRole(n string, h []string, p string, g []mysqlctl.RoleGrant) error {
	return m.spec.UpdateRole(n, h, p, g)
}
func (m mongoRoles) DropRole(n string) error  { return m.spec.DropRole(n) }
func (m mongoRoles) ValidName(n string) error { return mongoctl.ValidRoleName(n) }
func (m mongoRoles) ValidLevel(l string) bool { return mongoctl.ValidLevel(l) }
func (m mongoRoles) Levels() []string         { return mongoctl.Levels() }
func (m mongoRoles) SystemUser() string       { return mongoctl.RootUser }

func (s *Server) roleManagerFor(in store.Instance) (roleManager, error) {
	if in.Engine == engineMongoDB {
		spec, err := s.mongoSpec(in)
		if err != nil {
			return nil, err
		}
		return mongoRoles{spec: spec}, nil
	}
	if in.Engine == enginePostgres {
		spec, err := s.pgSpec(in)
		if err != nil {
			return nil, err
		}
		return pgRoles{spec: spec}, nil
	}
	spec, err := s.mysqlSpec(in)
	if err != nil {
		return nil, err
	}
	return mysqlRoles{spec: spec}, nil
}

// handleListUsers 列数据库用户。
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	mgr, err := s.roleManagerFor(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	roles, err := mgr.Roles()
	if isNotRunning(err) {
		s.fail(w, r, errState("实例未运行，请先启动再管理用户"))
		return
	}
	if err != nil {
		s.fail(w, r, errInternal("读取用户失败："+err.Error()))
		return
	}
	writeOK(w, r, map[string]any{
		"items":  roles,
		"total":  len(roles),
		"levels": mgr.Levels(),
	})
}

// handleCreateUser 新建数据库账号，并可选地一次授好若干库。
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	mgr, err := s.roleManagerFor(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	var req createUserReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if err := mgr.ValidName(name); err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	var grantsPtr *[]roleGrantReq
	if req.Grants != nil {
		grantsPtr = &req.Grants
	}
	grants := grantsOf(grantsPtr, req.Database, req.Level)
	for _, g := range grants {
		if !mgr.ValidLevel(g.Level) {
			s.fail(w, r, errBadRequest("权限档位只能是 read / write / all / none"))
			return
		}
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	// PG 没有 user@host：来源由 pg_hba.conf 决定，这里既不校验也不保存
	hosts := req.Hosts
	if in.Engine == enginePostgres || in.Engine == engineMongoDB {
		hosts = nil
	}
	for _, h := range mysqlctl.NormalizeHosts(hosts) {
		if err := mysqlctl.ValidHost(h); err != nil {
			s.fail(w, r, errBadRequest(err.Error()))
			return
		}
	}

	if err := mgr.CreateRole(name, hosts, req.Password, grants); err != nil {
		// 审计只记用户名与授权，绝不落密码
		s.audit(r, id, "dbuser.create", "instance", in.ID, "failed", name+"（"+grantText(grants)+"）；"+err.Error())
		if isNotRunning(err) {
			s.fail(w, r, errState("实例未运行，请先启动再管理账号"))
			return
		}
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	// 密码留一份在本机：用户过几天要配客户端时还能查回来
	s.writeUserSecret(in.ID, name, req.Password)
	s.audit(r, id, "dbuser.create", "instance", in.ID, "ok", name+"（"+grantText(grants)+"）")
	outHosts := mysqlctl.NormalizeHosts(hosts)
	if in.Engine == enginePostgres || in.Engine == engineMongoDB {
		outHosts = nil
	}
	writeCreated(w, r, map[string]any{
		"name":   name,
		"grants": grants,
		"hosts":  outHosts,
	})
}

// handleUpdateUser 改密码与库授权。
//
// 请求里 grants 字段决定授权怎么变：不传 = 不动；传数组 = 以这份为准（空数组即撤销全部）。
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	mgr, err := s.roleManagerFor(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		s.fail(w, r, errBadRequest("缺少用户名"))
		return
	}
	if strings.EqualFold(name, mgr.SystemUser()) {
		s.fail(w, r, errBadRequest("系统账号 "+mgr.SystemUser()+" 不能在用户管理中修改"))
		return
	}

	var req updateUserReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	// 只有显式传了 grants（或老表单的 database）才算"要改授权"
	changeGrants := req.Grants != nil || strings.TrimSpace(req.Database) != ""
	var grants []mysqlctl.RoleGrant
	if changeGrants {
		grants = grantsOf(req.Grants, req.Database, req.Level)
		for _, g := range grants {
			if !mgr.ValidLevel(g.Level) {
				s.fail(w, r, errBadRequest("权限档位只能是 read / write / all / none"))
				return
			}
		}
	}
	// PG 没有来源概念，传了也当没传（否则会记一条"来源改为"的空变更）
	changeHosts := req.Hosts != nil && in.Engine != enginePostgres && in.Engine != engineMongoDB
	if req.Password == "" && !changeGrants && !changeHosts {
		s.fail(w, r, errBadRequest("没有需要修改的内容"))
		return
	}
	var hosts []string
	if changeHosts && in.Engine != enginePostgres && in.Engine != engineMongoDB {
		hosts = mysqlctl.NormalizeHosts(*req.Hosts)
		for _, h := range hosts {
			if err := mysqlctl.ValidHost(h); err != nil {
				s.fail(w, r, errBadRequest(err.Error()))
				return
			}
		}
	}
	// 新增来源时要建账号，必须有密码可用：没填就用该用户已存的那份
	password := req.Password
	if changeHosts && password == "" {
		password = s.readUserSecret(in.ID, name)
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	if err := mgr.UpdateRole(name, hosts, password, grants); err != nil {
		s.audit(r, id, "dbuser.update", "instance", in.ID, "failed", name+"；"+err.Error())
		if isNotRunning(err) {
			s.fail(w, r, errState("实例未运行，请先启动再管理账号"))
		} else {
			s.fail(w, r, errBadRequest(err.Error()))
		}
		return
	}
	var changes []string
	if req.Password != "" {
		changes = append(changes, "重设密码")
		s.writeUserSecret(in.ID, name, req.Password)
	}
	if changeHosts {
		changes = append(changes, "来源改为 "+strings.Join(hosts, "、"))
	}
	if changeGrants {
		changes = append(changes, "授权改为 "+grantText(grants))
	}
	s.audit(r, id, "dbuser.update", "instance", in.ID, "ok", name+"："+strings.Join(changes, "、"))
	writeOK(w, r, map[string]any{"name": name, "changes": changes})
}

// grantText 把授权列表压成一句给审计看的话，不含密码。
func grantText(grants []mysqlctl.RoleGrant) string {
	if len(grants) == 0 {
		return "未授权任何库"
	}
	parts := make([]string, 0, len(grants))
	for _, g := range grants {
		parts = append(parts, g.Database+"/"+g.Level)
	}
	return strings.Join(parts, "、")
}

// handleDeleteUser 删除数据库用户。
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	mgr, err := s.roleManagerFor(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		s.fail(w, r, errBadRequest("缺少用户名"))
		return
	}
	if strings.EqualFold(name, mgr.SystemUser()) {
		s.fail(w, r, errBadRequest("系统账号 "+mgr.SystemUser()+" 不能删除"))
		return
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	if err := mgr.DropRole(name); err != nil {
		s.audit(r, id, "dbuser.delete", "instance", in.ID, "failed", name+"；"+err.Error())
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	s.audit(r, id, "dbuser.delete", "instance", in.ID, "ok", name)
	writeOK(w, r, map[string]any{"name": name})
}

// handleRevealUserPassword 取回账号密码。
//
// MySQL 里只有密文，回显的是创建/重设时留在本机的那一份（0600，仅本机可读）。
// 客户端里改过密码、或从别处导入的服务会没有这一份，此时如实报"没有记录"。
func (s *Server) handleRevealUserPassword(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, true); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		s.fail(w, r, errBadRequest("缺少用户名"))
		return
	}
	pwd := s.readUserSecret(in.ID, name)
	w.Header().Set("Cache-Control", "no-store")
	if pwd == "" {
		s.fail(w, r, errNotFound("这个账号没有保存的密码（可能是外部工具创建的），只能重设"))
		return
	}
	writeOK(w, r, map[string]any{"name": name, "password": pwd})
}
