package mongoctl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	ConfigFileName    = "mongod.conf"
	ContainerConfDir  = "/etc/mongodb"
	ContainerConfFile = ContainerConfDir + "/" + ConfigFileName
	containerDBPath   = "/data/db"
)

// ConfigTemplate 是 MongoDB 6/7/8 共用的最小安全配置。
//
// 不写 systemLog 文件路径，日志继续走 Docker stdout，避免镜像内目录/属主差异。
func ConfigTemplate(series string) string {
	return fmt.Sprintf(`# 牛库管家生成：MongoDB %s
# 挂载到容器的 /etc/mongodb/mongod.conf；保存后需要重启服务生效。
storage:
  dbPath: %s

net:
  bindIp: 0.0.0.0
  port: 27017

security:
  authorization: enabled
`, strings.TrimSpace(series), containerDBPath)
}

func EnsureConfig(path, series string) error {
	if _, err := os.Stat(path); err == nil {
		return os.Chmod(path, 0o644)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(ConfigTemplate(series)), 0o644); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

// ValidateConfig 拦住会破坏容器挂载或关闭认证的修改。
func ValidateConfig(content string) error {
	if strings.TrimSpace(content) == "" {
		return errors.New("mongod.conf 不能为空")
	}
	if strings.ContainsRune(content, 0) {
		return errors.New("mongod.conf 不能包含二进制内容")
	}
	var cfg struct {
		Storage struct {
			DBPath string `yaml:"dbPath"`
		} `yaml:"storage"`
		Net struct {
			BindIP string `yaml:"bindIp"`
			Port   int    `yaml:"port"`
		} `yaml:"net"`
		Security struct {
			Authorization string `yaml:"authorization"`
		} `yaml:"security"`
	}
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		return fmt.Errorf("mongod.conf 不是合法 YAML：%w", err)
	}
	if cfg.Storage.DBPath != containerDBPath {
		return fmt.Errorf("storage.dbPath 必须是 %s，否则容器数据目录会失去挂载", containerDBPath)
	}
	if cfg.Net.Port != 27017 {
		return errors.New("net.port 必须是 27017；宿主机端口由部署配置单独分配")
	}
	if strings.TrimSpace(cfg.Net.BindIP) == "" {
		return errors.New("net.bindIp 不能为空")
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.Security.Authorization), "enabled") {
		return errors.New("security.authorization 必须为 enabled，不能关闭数据库认证")
	}
	return nil
}
