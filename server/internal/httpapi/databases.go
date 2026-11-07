package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"niudbm/server/internal/mysqlctl"
	"niudbm/server/internal/store"
)

// 一个"服务"（MySQL 容器）下的多个"数据库"（schema）。

type createDatabaseReq struct {
	Name    string `json:"name"`
	Charset string `json:"charset"`
	// Users 是只对这个库授权的账号：建库与发账号一次做完
	Users []createUserReq `json:"users"`
}

// handleListDatabases 列出该服务里的逻辑库。
func (s *Server) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	spec, err := s.dbSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	items, err := spec.ListDatabases()
	if isNotRunning(err) {
		s.fail(w, r, errState("实例未运行，请先启动再查看实例列表"))
		return
	}
	if err != nil {
		s.fail(w, r, errInternal("读取实例列表失败："+err.Error()))
		return
	}
	writeOK(w, r, map[string]any{"items": items, "total": len(items)})
}

// handleListCharsets 列出这台实例可选的库字符集。
//
// 清单来自引擎自己的 information_schema，不写死：gb18030、utf8mb3 这类字符集
// 在不同版本上出现的时间不同，写死一份迟早和某个版本对不上。
func (s *Server) handleListCharsets(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	spec, err := s.dbSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	items, err := spec.Charsets()
	if isNotRunning(err) {
		s.fail(w, r, errState("实例未运行，请先启动再查看字符集"))
		return
	}
	if err != nil {
		s.fail(w, r, errInternal("读取字符集失败："+err.Error()))
		return
	}
	writeOK(w, r, map[string]any{"items": items, "total": len(items)})
}

// handleCreateDatabase 新建一个逻辑库（界面上的"新建实例"）。
func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	var req createDatabaseReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if err := validDBName(in, name); err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	users, uerr := prepareNewUsers(in, req.Users)
	if uerr != nil {
		s.fail(w, r, errBadRequest(uerr.Error()))
		return
	}

	spec, err := s.dbSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	if err := spec.CreateDatabase(name, req.Charset); err != nil {
		s.audit(r, id, "database.create", "instance", in.ID, "failed", name+"；"+err.Error())
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	// 库建好后立刻授权；授权失败就把库撤掉，避免留下一个没人能访问的残库
	for _, u := range users {
		if err := spec.CreateRoleOn(name, u.Name, nil, u.Password, u.Level); err != nil {
			_ = spec.DropDatabase(name)
			s.audit(r, id, "database.create", "instance", in.ID, "failed",
				name+"；创建账号 "+u.Name+" 失败："+err.Error())
			s.fail(w, r, errBadRequest("创建账号 "+u.Name+" 失败："+err.Error()))
			return
		}
		s.writeUserSecret(in.ID, u.Name, u.Password)
	}
	detail := name
	if len(users) > 0 {
		names := make([]string, 0, len(users))
		for _, u := range users {
			names = append(names, u.Name)
		}
		detail += "，授权给 " + strings.Join(names, "、")
	}
	s.audit(r, id, "database.create", "instance", in.ID, "ok", detail)
	writeCreated(w, r, map[string]any{"name": name, "charset": req.Charset})
}

// prepareNewUsers 校验"建库时一并建号"的入参。
//
// 空白行（名字与密码都没填）直接跳过，前端加了一行没填也不会报错；
// 只要填了名字就必须完整合法。这里做第一道校验，建用户时引擎侧还会再校验一次。
func prepareNewUsers(in store.Instance, raw []createUserReq) ([]createUserReq, error) {
	seen := make(map[string]bool, len(raw))
	out := make([]createUserReq, 0, len(raw))
	for _, u := range raw {
		name := strings.TrimSpace(u.Name)
		if name == "" && u.Password == "" {
			continue
		}
		if name == "" {
			return nil, errors.New("接入用户的用户名不能为空")
		}
		if err := validRoleNameFor(in, name); err != nil {
			return nil, err
		}
		if seen[strings.ToLower(name)] {
			return nil, errors.New("接入用户名重复：" + name)
		}
		seen[strings.ToLower(name)] = true

		level := strings.TrimSpace(u.Level)
		if level == "" {
			level = mysqlctl.LevelRead
		}
		if !mysqlctl.ValidLevel(level) {
			return nil, errors.New(name + " 的权限档位只能是 read / write / all / none")
		}
		// 密码不剪空白：前后空格也可能是密码的一部分
		if len(u.Password) < 8 {
			return nil, errors.New(name + " 的密码至少 8 位")
		}
		out = append(out, createUserReq{Name: name, Password: u.Password, Level: level})
	}
	return out, nil
}

// handleCreateDatabaseUser 给已经存在的库补一个只对它授权的账号。
//
// 建库时一并建号是主路径，但"库早就建好了、后来才要加个账号"同样常见。
// 以前只能去「用户」页建，而那里建出来的账号只授默认库，
// 于是出现"账号建好了却连不上这个库"——这个接口补的就是这个缺口。
func (s *Server) handleCreateDatabaseUser(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	dbName := strings.TrimSpace(r.PathValue("name"))
	if err := validDBName(in, dbName); err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	var req createUserReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if err := validRoleNameFor(in, name); err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	level := strings.TrimSpace(req.Level)
	if level == "" {
		level = mysqlctl.LevelRead
	}
	if !mysqlctl.ValidLevel(level) {
		s.fail(w, r, errBadRequest("权限档位只能是 read / write / all / none"))
		return
	}
	if len(req.Password) < 8 {
		s.fail(w, r, errBadRequest("密码至少 8 位"))
		return
	}

	spec, err := s.dbSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	// 库必须真的存在：否则会建出一个在不存在库上授权的账号，用户拿它连接只会报 1044
	dbs, lerr := spec.ListDatabases()
	if isNotRunning(lerr) {
		s.fail(w, r, errState("实例未运行，请先启动再管理账号"))
		return
	}
	if lerr != nil {
		s.fail(w, r, errInternal("读取库列表失败："+lerr.Error()))
		return
	}
	found := false
	for _, d := range dbs {
		if strings.EqualFold(d.Name, dbName) {
			found = true
			break
		}
	}
	if !found {
		s.fail(w, r, errNotFound("库 "+dbName+" 不存在，先建库再建账号"))
		return
	}

	if err := spec.CreateRoleOn(dbName, name, req.Hosts, req.Password, level); err != nil {
		s.audit(r, id, "dbuser.create", "instance", in.ID, "failed", dbName+"："+name+"（"+level+"）；"+err.Error())
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	s.writeUserSecret(in.ID, name, req.Password)
	s.audit(r, id, "dbuser.create", "instance", in.ID, "ok", dbName+"："+name+"（"+level+"）")
	writeCreated(w, r, map[string]any{
		"name":     name,
		"level":    level,
		"database": dbName,
		"hosts":    mysqlctl.NormalizeHosts(req.Hosts),
	})
}

// handleDropDatabase 删除非默认库；默认库由引擎层拒绝。
func (s *Server) handleDropDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	// 删库是不可逆的：要求把库名再输一遍，和删实例保持同一套确认习惯
	confirm := r.URL.Query().Get("confirmName")
	if confirm != name {
		s.fail(w, r, errRejected("请输入库名以确认删除"))
		return
	}

	spec, err := s.dbSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	if err := spec.DropDatabase(name); err != nil {
		s.audit(r, id, "database.delete", "instance", in.ID, "failed", name+"；"+err.Error())
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	s.audit(r, id, "database.delete", "instance", in.ID, "ok", name)
	writeOK(w, r, map[string]any{"name": name, "deleted": true})
}
