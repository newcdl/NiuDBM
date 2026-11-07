package pgctl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Backup 把一个库做逻辑备份：pg_dump 出来的纯 SQL，跨版本、跨机器都能导入。
//
// 备份按库进行：一个服务里可以有多个库，整实例备份既大又难挑出要恢复的那个。
// 实例原本停着时先拉起来，备完再停回去，用户感知不到状态变化。
func (s Spec) Backup(dst, database string) error {
	if s.External {
		return errors.New("外部连接暂不支持由本应用备份，请使用数据库自身的备份方案")
	}
	database = strings.TrimSpace(database)
	if database == "" {
		return errors.New("请选择要备份的库")
	}
	if err := ValidDBName(database); err != nil {
		return err
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

// dump 用容器自带的 pg_dump 导出指定库。
//
// --create 让文件里带 CREATE DATABASE，恢复时不需要先手工建库；
// --clean --if-exists 让重复恢复变成"覆盖"而不是一路报错。
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

	if err := s.Docker.Client.ExecStream(ctx, s.Docker.Container, out, nil,
		map[string]string{"PGPASSWORD": s.Password},
		"pg_dump", "-U", SuperUser, "-d", database,
		"--create", "--clean", "--if-exists"); err != nil {
		return fmt.Errorf("导出数据库失败：%w", err)
	}
	return nil
}

// Verify 校验备份文件确实是一次 PG 导出。
//
// 与 MySQL 的校验同一套思路：找导出头与建表/建库语句。
// 空库的备份里不会有 CREATE TABLE，但有 CREATE DATABASE 与导出头，仍算有效。
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
		if strings.Contains(line, "PostgreSQL database dump") {
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
	if !hasDumpHead {
		return errors.New("备份文件缺少 PostgreSQL 导出头，可能被截断")
	}
	return nil
}

// VerifyBackup 让上层通过统一运行时接口校验备份。
func (s Spec) VerifyBackup(path string) error { return Verify(path) }

// Restore 用备份文件覆盖同名库；恢复后实例保持运行。
//
// 连的是 postgres 维护库：dump 里带 \connect 与 CREATE DATABASE，
// 在业务库上下文里执行会因为没有该库而失败。
func (s Spec) Restore(src string) error {
	if s.External {
		return errors.New("外部连接暂不支持由本应用恢复，请使用数据库自身的恢复方案")
	}
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
	if err := s.Docker.Client.ExecStream(ctx, s.Docker.Container, nil, f,
		map[string]string{"PGPASSWORD": s.Password},
		"psql", "-U", SuperUser, "-d", RootUser, "-v", "ON_ERROR_STOP=1", "-q", "-f", "-"); err != nil {
		return fmt.Errorf("恢复失败：%w", err)
	}
	return nil
}
