package mysqlctl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Backup 把一个库做逻辑备份：dump 出来是 SQL，跨版本、跨机器都能导入。
//
// 备份按库进行——一个服务里可以有多个库，库之间可能属于不同业务，
// 整实例备份的 SQL 文件既大又难挑出来恢复。所以必须指明库：
// 没有"默认库"可以兜底，服务建出来就是空的。
// 备份要求实例处于运行态；原本停着的实例备份完会回到停止状态，
// 用户感知不到状态变化。
func (s Spec) Backup(dst, database string) error {
	database = strings.TrimSpace(database)
	if database == "" {
		return errors.New("请选择要备份的库")
	}
	wasRunning := s.Running()
	if !wasRunning {
		if err := s.Start(); err != nil {
			return fmt.Errorf("备份前启动实例失败：%w", err)
		}
	}

	dumpErr := s.dump(dst, database)

	if !wasRunning {
		_ = s.Stop()
	}
	if dumpErr != nil {
		_ = os.Remove(dst)
		return dumpErr
	}
	return nil
}

// dump 用容器自带的 dump 工具导出指定库。
//
// MariaDB 镜像里叫 mariadb-dump，MySQL 镜像里叫 mysqldump，两边都保留了 mysql 软链，
// 这里按顺序试两个名字；密码走 MYSQL_PWD 环境变量，不出现在命令行里。
func (s Spec) dump(dst, database string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	defer out.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	flags := []string{
		"--single-transaction", "--routines", "--triggers", "--events",
		"--add-drop-database", "--databases", database, "-u" + SuperUser,
	}
	var lastErr error
	for _, tool := range []string{"mariadb-dump", "mysqldump"} {
		if err := s.Docker.Client.ExecStream(ctx, s.Docker.Container, out, nil,
			map[string]string{"MYSQL_PWD": s.Password}, append([]string{tool}, flags...)...); err != nil {
			lastErr = err
			// 换工具重试时要清掉半个文件，避免拼出坏备份
			if _, seekErr := out.Seek(0, 0); seekErr == nil {
				_ = out.Truncate(0)
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("导出数据库失败：%w", lastErr)
}

// Verify 校验备份文件完整且确实是一次数据库导出。
func Verify(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	hasCreate, hasDumpHead := false, false
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "CREATE DATABASE") || strings.Contains(line, "CREATE TABLE") {
			hasCreate = true
		}
		if strings.Contains(line, "MariaDB dump") || strings.Contains(line, "MySQL dump") {
			hasDumpHead = true
		}
		if hasCreate && hasDumpHead {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("备份文件损坏：%w", err)
	}
	if !hasCreate {
		return errors.New("备份文件里没有建库/建表语句，可能不是有效的导出")
	}
	return nil
}

// Restore 用备份文件覆盖默认库；恢复后实例保持运行，由上层决定后续状态。
func (s Spec) Restore(src string) error {
	if err := Verify(src); err != nil {
		return err
	}
	if !s.Running() {
		if err := s.Start(); err != nil {
			return fmt.Errorf("恢复前启动实例失败：%w", err)
		}
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	// 镜像里的客户端命令不统一：两个名字都试一次，成功即返回
	var lastErr error
	for _, tool := range []string{"mysql", "mariadb"} {
		if _, seekErr := f.Seek(0, 0); seekErr != nil {
			return seekErr
		}
		err = s.Docker.Client.ExecStream(ctx, s.Docker.Container, io.Discard, f,
			map[string]string{"MYSQL_PWD": s.Password}, tool, "-u"+SuperUser)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("导入备份失败：%w", lastErr)
}
