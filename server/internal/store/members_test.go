package store

import (
	"context"
	"testing"
)

func TestInstanceMembers(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	member := InstanceMember{
		InstanceID: "I1", UserID: "1001", Username: "alice", Role: MemberRoleViewer,
	}
	if err := s.UpsertInstanceMember(ctx, member); err != nil {
		t.Fatal(err)
	}
	role, err := s.InstanceMemberRole(ctx, "I1", "1001")
	if err != nil || role != MemberRoleViewer {
		t.Fatalf("读取角色失败：role=%q err=%v", role, err)
	}
	member.Role = MemberRoleOperator
	if err := s.UpsertInstanceMember(ctx, member); err != nil {
		t.Fatal(err)
	}
	roles, err := s.InstanceIDsForMember(ctx, "1001")
	if err != nil || roles["I1"] != MemberRoleOperator {
		t.Fatalf("服务角色映射不对：%v %v", roles, err)
	}
	if err := s.DeleteInstanceMember(ctx, "I1", "1001"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InstanceMemberRole(ctx, "I1", "1001"); err != ErrNotFound {
		t.Fatalf("删除后应返回 ErrNotFound，实际 %v", err)
	}
}

func TestDeployTaskPersistence(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	task := DeployTask{
		ID: "D1", Name: "mongo", Engine: "mongodb", Phase: "prepare",
		PhaseText: "准备中", Status: "running", StartedAt: 1,
	}
	if err := s.CreateDeployTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	task.Status = "failed"
	task.Phase = "failed"
	task.Error = "中断"
	task.FinishedAt = 2
	if err := s.UpdateDeployTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDeployTask(ctx, "D1")
	if err != nil || got.Status != "failed" || got.Error != "中断" {
		t.Fatalf("部署任务未持久化：%+v %v", got, err)
	}
}
