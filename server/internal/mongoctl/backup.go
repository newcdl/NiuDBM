package mongoctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func (s Spec) Backup(dst, database string) error {
	if s.External {
		return errors.New("外部 MongoDB 暂不支持由本应用备份")
	}
	if err := ValidDBName(database); err != nil {
		return err
	}
	if !s.Running() {
		if err := s.Start(); err != nil {
			return err
		}
	}
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
	args := []string{
		"mongodump", "--uri", s.uri(), "--db", database, "--archive", "--gzip",
	}
	if err := s.Docker.Client.ExecStream(ctx, s.Docker.Container, out, nil, nil, args...); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("MongoDB 备份失败：%w", err)
	}
	return nil
}

func (s Spec) Restore(src string) error {
	if s.External {
		return errors.New("外部 MongoDB 暂不支持由本应用恢复")
	}
	if err := s.VerifyBackup(src); err != nil {
		return err
	}
	if !s.Running() {
		if err := s.Start(); err != nil {
			return err
		}
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	args := []string{
		"mongorestore", "--uri", s.uri(), "--archive", "--gzip", "--drop",
	}
	if err := s.Docker.Client.ExecStream(ctx, s.Docker.Container, io.Discard, f, nil, args...); err != nil {
		return fmt.Errorf("MongoDB 恢复失败：%w", err)
	}
	return nil
}

func (s Spec) VerifyBackup(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 2)
	n, err := io.ReadFull(f, head)
	if err != nil || n != 2 {
		return errors.New("MongoDB 备份文件为空或已截断")
	}
	if head[0] != 0x1f || head[1] != 0x8b {
		return errors.New("MongoDB 备份文件不是有效的 gzip archive")
	}
	fi, err := f.Stat()
	if err != nil || fi.Size() < 32 {
		return errors.New("MongoDB 备份文件过小，可能已损坏")
	}
	return nil
}
