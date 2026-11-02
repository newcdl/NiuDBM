package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "meta", "niudbm.db"))
	if err != nil {
		t.Fatalf("打开元数据库失败：%v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sampleInstance(id, name string) Instance {
	now := time.Now().UnixMilli()
	return Instance{
		ID: id, Name: name, Engine: "sqlite", Mode: "managed", Status: "running",
		DataDir: filepath.ToSlash(filepath.Join(id, "data.db")), Profile: "low",
		CreatedAt: now, UpdatedAt: now,
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "niudbm.db")

	for i := 0; i < 3; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("第 %d 次打开失败：%v", i+1, err)
		}
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&n); err != nil {
			t.Fatalf("读取迁移记录失败：%v", err)
		}
		if n != 1 {
			t.Fatalf("迁移记录应只有 1 条，得到 %d", n)
		}
		_ = s.Close()
	}
}

func TestInstanceCRUD(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	in := sampleInstance("ID1", "博客库")
	if err := s.CreateInstance(ctx, in); err != nil {
		t.Fatalf("创建失败：%v", err)
	}

	n, err := s.CountInstances(ctx)
	if err != nil || n != 1 {
		t.Fatalf("计数不对：n=%d err=%v", n, err)
	}

	got, err := s.GetInstance(ctx, "ID1")
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if got.Name != "博客库" || got.DataDir != "ID1/data.db" {
		t.Fatalf("字段不符：%+v", got)
	}

	if err := s.RenameInstance(ctx, "ID1", "博客库2"); err != nil {
		t.Fatalf("改名失败：%v", err)
	}
	got, _ = s.GetInstance(ctx, "ID1")
	if got.Name != "博客库2" {
		t.Fatalf("改名未生效：%s", got.Name)
	}

	list, err := s.ListInstances(ctx, "博客", "")
	if err != nil || len(list) != 1 {
		t.Fatalf("模糊查询不对：len=%d err=%v", len(list), err)
	}
	list, _ = s.ListInstances(ctx, "不存在", "")
	if len(list) != 0 {
		t.Fatalf("不应命中：%+v", list)
	}

	if err := s.SoftDelete(ctx, got); err != nil {
		t.Fatalf("删除失败：%v", err)
	}
	if _, err := s.GetInstance(ctx, "ID1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后应查不到，得到：%v", err)
	}
	if n, _ := s.CountInstances(ctx); n != 0 {
		t.Fatalf("删除后计数应为 0，得到 %d", n)
	}

	// 删除后同名可以重新创建（台账内部把旧名改写掉了）
	if err := s.CreateInstance(ctx, sampleInstance("ID2", "博客库2")); err != nil {
		t.Fatalf("同名重建失败：%v", err)
	}
}

func TestDuplicateName(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if err := s.CreateInstance(ctx, sampleInstance("A", "重复")); err != nil {
		t.Fatal(err)
	}
	err := s.CreateInstance(ctx, sampleInstance("B", "重复"))
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("应返回 ErrNameTaken，得到：%v", err)
	}
}

func TestMissingInstance(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.GetInstance(ctx, "NOPE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，得到：%v", err)
	}
	if err := s.RenameInstance(ctx, "NOPE", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("改名不存在实例应返回 ErrNotFound，得到：%v", err)
	}
}

// 配额与存储方式要能往返：漏掉列会导致改完配额、刷新页面又变回"不限制"。
func TestInstanceQuotaRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	in := sampleInstance("Q1", "带配额的库")
	in.Engine = "mysql"
	in.Runtime = "docker"
	in.Port = 33061
	in.CPULimit = 2
	in.MemLimitMB = 1024
	in.StorageKind = "bind"
	in.StoragePath = "/vol1/mysql/q1"
	if err := s.CreateInstance(ctx, in); err != nil {
		t.Fatalf("创建失败：%v", err)
	}

	got, err := s.GetInstance(ctx, "Q1")
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if got.CPULimit != 2 || got.MemLimitMB != 1024 {
		t.Fatalf("配额没存住：cpu=%v mem=%d", got.CPULimit, got.MemLimitMB)
	}
	if got.StorageKind != "bind" || got.StoragePath != "/vol1/mysql/q1" {
		t.Fatalf("存储方式没存住：kind=%q path=%q", got.StorageKind, got.StoragePath)
	}

	if err := s.UpdateLimits(ctx, "Q1", 0.5, 512); err != nil {
		t.Fatalf("改配额失败：%v", err)
	}
	got, err = s.GetInstance(ctx, "Q1")
	if err != nil {
		t.Fatalf("二次读取失败：%v", err)
	}
	if got.CPULimit != 0.5 || got.MemLimitMB != 512 {
		t.Fatalf("配额没更新：cpu=%v mem=%d", got.CPULimit, got.MemLimitMB)
	}

	// 老数据（空 StorageKind）读出来要收敛成 volume，界面才不会显示成空白
	old := sampleInstance("Q2", "老库")
	if err := s.CreateInstance(ctx, old); err != nil {
		t.Fatalf("创建老实例失败：%v", err)
	}
	got, err = s.GetInstance(ctx, "Q2")
	if err != nil {
		t.Fatalf("读取老实例失败：%v", err)
	}
	if got.StorageKind != "volume" || got.CPULimit != 0 || got.MemLimitMB != 0 {
		t.Fatalf("默认值不对：kind=%q cpu=%v mem=%d", got.StorageKind, got.CPULimit, got.MemLimitMB)
	}
}

func TestBackupRecords(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.CreateInstance(ctx, sampleInstance("I1", "库")); err != nil {
		t.Fatal(err)
	}

	rec := Backup{ID: "B1", InstanceID: "I1", Type: "manual", Status: "running",
		StoragePath: "/backups/I1/snap-1.db", Database: "shop", StartedAt: time.Now().UnixMilli()}
	if err := s.CreateBackup(ctx, rec); err != nil {
		t.Fatalf("写入备份记录失败：%v", err)
	}

	rec.Status = "succeeded"
	rec.SizeBytes = 4096
	rec.Checksum = "abc"
	rec.FinishedAt = time.Now().UnixMilli()
	if err := s.FinishBackup(ctx, rec); err != nil {
		t.Fatalf("回填备份记录失败：%v", err)
	}

	list, err := s.ListBackups(ctx, "I1")
	if err != nil || len(list) != 1 {
		t.Fatalf("列表不对：len=%d err=%v", len(list), err)
	}
	if list[0].FileName != "snap-1.db" || list[0].Status != "succeeded" || list[0].SizeBytes != 4096 {
		t.Fatalf("字段不符：%+v", list[0])
	}
	// 备份按库进行：库名必须能读回来，恢复时要靠它回到同一个库
	if list[0].Database != "shop" {
		t.Fatalf("备份的库名没存住：%+v", list[0])
	}

	// 跨实例取记录必须查不到
	if _, err := s.GetBackup(ctx, "OTHER", "B1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨实例访问应拒绝，得到：%v", err)
	}

	if err := s.DeleteBackup(ctx, "B1"); err != nil {
		t.Fatalf("删除失败：%v", err)
	}
	list, _ = s.ListBackups(ctx, "I1")
	if len(list) != 0 {
		t.Fatalf("删除后应为空：%+v", list)
	}
}

func TestAuditAppendAndList(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := s.AddAudit(ctx, Audit{
			UserID: "u1", Username: "小柿子", Action: "instance.create",
			TargetKind: "instance", TargetID: "I1", Result: "ok", Detail: "测试",
		}); err != nil {
			t.Fatalf("写审计失败：%v", err)
		}
	}

	list, err := s.ListAudit(ctx, 3)
	if err != nil || len(list) != 3 {
		t.Fatalf("条数不对：len=%d err=%v", len(list), err)
	}
	if list[0].ID <= list[1].ID {
		t.Fatalf("应按 id 倒序：%d, %d", list[0].ID, list[1].ID)
	}
}
