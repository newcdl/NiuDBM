package httpapi

import (
	"encoding/json"
	"log"
	"net/http"

	"niudbm/server/internal/ulid"
)

// 错误码分段与《数据模型与接口契约》§5.2 保持一致；新增值只追加，不复用。
const (
	codeOK = 0

	codeBadRequest   = 10001
	codeUnauthorized = 10002
	codeNotFound     = 10004

	codeInstanceNotFound = 20001
	codeStateNotAllowed  = 20002

	codeBackupTarget = 50001
	codeBackupVerify = 50002
	codeRestoreDeny  = 50003
)

// envelope 是所有接口的统一响应包。
type envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
	TraceID string `json:"traceId"`
}

// apiErr 携带 HTTP 状态码与业务码，便于一层处理。
type apiErr struct {
	Status  int
	Code    int
	Message string
}

func (e apiErr) Error() string { return e.Message }

func errBadRequest(msg string) apiErr { return apiErr{http.StatusBadRequest, codeBadRequest, msg} }
func errUnauthorized(msg string) apiErr {
	return apiErr{http.StatusUnauthorized, codeUnauthorized, msg}
}
func errNotFound(msg string) apiErr { return apiErr{http.StatusNotFound, codeNotFound, msg} }
func errInstanceNotFound() apiErr {
	return apiErr{http.StatusNotFound, codeInstanceNotFound, "实例不存在或已被删除"}
}
func errState(msg string) apiErr    { return apiErr{http.StatusConflict, codeStateNotAllowed, msg} }
func errInternal(msg string) apiErr { return apiErr{http.StatusInternalServerError, 19999, msg} }
func errNotFoundAPI() apiErr        { return apiErr{http.StatusNotFound, codeNotFound, "接口不存在"} }
func errRejected(msg string) apiErr { return apiErr{http.StatusBadRequest, codeRestoreDeny, msg} }
func errBackupTarget(msg string) apiErr {
	return apiErr{http.StatusInternalServerError, codeBackupTarget, msg}
}
func errBackupVerify(msg string) apiErr { return apiErr{http.StatusConflict, codeBackupVerify, msg} }

func writeOK(w http.ResponseWriter, r *http.Request, data any) {
	writeEnvelope(w, r, http.StatusOK, envelope{Message: "ok", Data: data})
}

func writeCreated(w http.ResponseWriter, r *http.Request, data any) {
	writeEnvelope(w, r, http.StatusCreated, envelope{Message: "已创建", Data: data})
}

func writeErr(w http.ResponseWriter, r *http.Request, err apiErr) {
	if err.Status >= http.StatusInternalServerError {
		log.Printf("接口错误 %s %s：%s", r.Method, r.URL.Path, err.Message)
	}
	writeEnvelope(w, r, err.Status, envelope{Code: err.Code, Message: err.Message})
}

func writeEnvelope(w http.ResponseWriter, r *http.Request, status int, env envelope) {
	trace := ulid.New()
	env.TraceID = trace
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Trace-Id", trace)
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		log.Printf("写出 JSON 失败: %v", err)
	}
}

// decodeJSON 读取请求体；空体视为合法（用于 DELETE 之类只有查询参数的接口）。
func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errBadRequest("请求体不是合法 JSON：" + err.Error())
	}
	return nil
}
