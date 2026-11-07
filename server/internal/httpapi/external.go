package httpapi

import (
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"niudbm/server/internal/images"
	"niudbm/server/internal/mongoctl"
	"niudbm/server/internal/mysqlctl"
	"niudbm/server/internal/pgctl"
	"niudbm/server/internal/store"
	"niudbm/server/internal/ulid"
)

type connectInstanceReq struct {
	Name          string `json:"name"`
	Engine        string `json:"engine"`
	EngineVersion string `json:"engineVersion"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	TLSMode       string `json:"tlsMode"`
}

type externalEndpoint struct {
	Name          string
	Engine        string
	EngineVersion string
	Host          string
	Port          int
	Username      string
	Password      string
	TLSMode       string
}

// handleConnectInstance 把用户已有数据库纳入管理。
//
// 与创建托管服务不同，这条链路不依赖 Docker，也不会启停目标数据库。
// 连接验证通过后才落台账与凭据，避免列表里出现一条永远连不上的脏记录。
func (s *Server) handleConnectInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}

	var req connectInstanceReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	endpoint, verr := normalizeExternalEndpoint(req)
	if verr != nil {
		s.fail(w, r, verr)
		return
	}
	taken, err := s.st.NameTaken(r.Context(), endpoint.Name)
	if err != nil {
		s.fail(w, r, errInternal("检查服务名失败"))
		return
	}
	if taken {
		s.fail(w, r, errBadRequest("服务名已存在，请换一个"))
		return
	}

	in := store.Instance{
		ID:            ulid.New(),
		Name:          endpoint.Name,
		Engine:        endpoint.Engine,
		EngineVersion: endpoint.EngineVersion,
		Mode:          instanceModeExternal,
		Host:          endpoint.Host,
		Username:      endpoint.Username,
		TLSMode:       endpoint.TLSMode,
		Status:        "running",
		Profile:       "external",
		Port:          endpoint.Port,
		Runtime:       "external",
		CreatedAt:     time.Now().UnixMilli(),
		UpdatedAt:     time.Now().UnixMilli(),
	}
	if err := s.verifyExternal(endpoint); err != nil {
		s.fail(w, r, errState("连接失败："+err.Error()))
		return
	}
	if err := s.writeExternalSecret(in.ID, endpoint.Password); err != nil {
		s.fail(w, r, errInternal("保存连接凭据失败"))
		return
	}
	if err := s.st.CreateInstance(r.Context(), in); err != nil {
		_ = removeIfExists(s.externalSecretPath(in.ID))
		if errors.Is(err, store.ErrNameTaken) {
			s.fail(w, r, errBadRequest("服务名已存在，请换一个"))
			return
		}
		s.fail(w, r, errInternal("保存数据库服务失败"))
		return
	}

	detail := endpoint.Engine + " " + endpoint.EngineVersion + " " +
		net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)) + "，账号 " + endpoint.Username
	s.audit(r, id, "instance.connect", "instance", in.ID, "ok", detail)
	writeCreated(w, r, s.view(in))
}

// handleTestInstanceConnection 重新验证已保存的外部连接。
func (s *Server) handleTestInstanceConnection(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	if !isExternal(in) {
		s.fail(w, r, errBadRequest("托管服务会由 Docker 状态自动检测，不需要外部连接测试"))
		return
	}
	if err := s.verifyExternal(externalEndpoint{
		Engine:        in.Engine,
		EngineVersion: in.EngineVersion,
		Host:          in.Host,
		Port:          in.Port,
		Username:      in.Username,
		Password:      s.readExternalSecret(in.ID),
		TLSMode:       in.TLSMode,
	}); err != nil {
		s.fail(w, r, errState("连接失败："+err.Error()))
		return
	}
	writeOK(w, r, map[string]any{"connected": true, "message": "连接正常"})
}

func normalizeExternalEndpoint(req connectInstanceReq) (externalEndpoint, error) {
	name := strings.TrimSpace(req.Name)
	if err := validateName(name); err != nil {
		return externalEndpoint{}, err
	}
	engine := strings.TrimSpace(req.Engine)
	if engine != engineMySQL && engine != enginePostgres && engine != engineMongoDB {
		return externalEndpoint{}, errBadRequest("外部连接支持 MySQL、PostgreSQL 与 MongoDB")
	}
	host := strings.TrimSpace(req.Host)
	if err := validateExternalHost(host); err != nil {
		return externalEndpoint{}, errBadRequest(err.Error())
	}
	if req.Port < 1 || req.Port > 65535 {
		return externalEndpoint{}, errBadRequest("端口必须在 1-65535 之间")
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return externalEndpoint{}, errBadRequest("数据库账号不能为空")
	}
	if len(username) > 128 {
		return externalEndpoint{}, errBadRequest("数据库账号过长")
	}
	if req.Password == "" {
		return externalEndpoint{}, errBadRequest("数据库密码不能为空")
	}
	if len(req.Password) > 4096 {
		return externalEndpoint{}, errBadRequest("数据库密码过长")
	}
	tlsMode := strings.TrimSpace(req.TLSMode)
	if tlsMode == "" {
		tlsMode = "disable"
	}
	if tlsMode != "disable" && tlsMode != "require" {
		return externalEndpoint{}, errBadRequest("TLS 模式只能是 disable 或 require")
	}
	version := strings.TrimSpace(req.EngineVersion)
	if len([]rune(version)) > 32 {
		return externalEndpoint{}, errBadRequest("数据库版本最多 32 个字符")
	}
	return externalEndpoint{
		Name:          name,
		Engine:        engine,
		EngineVersion: version,
		Host:          host,
		Port:          req.Port,
		Username:      username,
		Password:      req.Password,
		TLSMode:       tlsMode,
	}, nil
}

func validateExternalHost(host string) error {
	if host == "" {
		return errors.New("数据库地址不能为空")
	}
	if len(host) > 253 {
		return errors.New("数据库地址过长")
	}
	if strings.ContainsAny(host, " /\\:@?#") {
		return errors.New("数据库地址只填主机名或 IP，不要带协议、端口或路径")
	}
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return errors.New("数据库地址格式不正确")
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '-' || r == '_' {
				continue
			}
			return errors.New("数据库地址只支持主机名或 IP")
		}
	}
	return nil
}

func testExternalConnection(endpoint externalEndpoint) error {
	switch endpoint.Engine {
	case engineMySQL:
		return (mysqlctl.Spec{
			Series:   endpoint.EngineVersion,
			Flavor:   images.FamilyMySQL,
			Host:     endpoint.Host,
			User:     endpoint.Username,
			External: true,
			TLSMode:  endpoint.TLSMode,
			Port:     endpoint.Port,
			Password: endpoint.Password,
		}).CheckConnection()
	case enginePostgres:
		return (pgctl.Spec{
			Series:   endpoint.EngineVersion,
			Flavor:   images.FamilyPostgres,
			Host:     endpoint.Host,
			User:     endpoint.Username,
			External: true,
			TLSMode:  endpoint.TLSMode,
			Port:     endpoint.Port,
			Password: endpoint.Password,
		}).CheckConnection()
	case engineMongoDB:
		return (mongoctl.Spec{
			Series: endpoint.EngineVersion, Flavor: images.FamilyMongo,
			Host: endpoint.Host, User: endpoint.Username, External: true,
			TLSMode: endpoint.TLSMode, Port: endpoint.Port, Password: endpoint.Password,
		}).CheckConnection()
	}
	return errors.New("无法识别数据库引擎")
}

func (s *Server) verifyExternal(endpoint externalEndpoint) error {
	if s.externalTest != nil {
		return s.externalTest(endpoint)
	}
	return testExternalConnection(endpoint)
}

func (s *Server) externalEndpointOf(in store.Instance) externalEndpoint {
	return externalEndpoint{
		Name:          in.Name,
		Engine:        in.Engine,
		EngineVersion: in.EngineVersion,
		Host:          in.Host,
		Port:          in.Port,
		Username:      in.Username,
		Password:      s.readExternalSecret(in.ID),
		TLSMode:       in.TLSMode,
	}
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
