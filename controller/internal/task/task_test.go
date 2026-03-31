// task 패키지 유닛 테스트
//
// 테스트 대상: NewTaskService, CreateTask, GetTask, ListTasks, DeleteTask, RunAsync
package task

import (
	"fmt"
	"testing"
	"time"
)

// TestNewTaskService — TaskService 생성 검증
func TestNewTaskService(t *testing.T) {
	t.Parallel()
	svc := NewTaskService()

	if svc == nil {
		t.Fatal("NewTaskService()가 nil을 반환함")
	}

	// 초기 상태: 비어 있음
	tasks := svc.ListTasks("", "")
	if len(tasks) != 0 {
		t.Errorf("초기 목록 길이: got %d, want 0", len(tasks))
	}
}

// TestCreateTask — 태스크 생성 후 필드 검증
func TestCreateTask(t *testing.T) {
	t.Parallel()
	svc := NewTaskService()

	task := svc.CreateTask("vm.migrate", 1)
	if task.ID == "" {
		t.Error("태스크 ID가 비어 있음")
	}
	if task.Type != "vm.migrate" {
		t.Errorf("Type: got %q, want %q", task.Type, "vm.migrate")
	}
	snap := task.Snapshot()
	if snap.Status != StatusPending {
		t.Errorf("Status: got %q, want %q", snap.Status, StatusPending)
	}
	if snap.VMID != 1 {
		t.Errorf("VMID: got %d, want 1", snap.VMID)
	}
	if snap.Progress != 0 {
		t.Errorf("Progress: got %d, want 0", snap.Progress)
	}
}

// TestGetTask — ID로 태스크 조회 검증
func TestGetTask(t *testing.T) {
	t.Parallel()
	svc := NewTaskService()

	created := svc.CreateTask("backup.create", 2)

	got, err := svc.GetTask(created.ID)
	if err != nil {
		t.Fatalf("GetTask() 에러: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID: got %q, want %q", got.ID, created.ID)
	}
}

// TestGetTaskNotFound — 존재하지 않는 태스크 조회 시 에러 반환
func TestGetTaskNotFound(t *testing.T) {
	t.Parallel()
	svc := NewTaskService()

	_, err := svc.GetTask("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 태스크에 대해 에러가 반환되어야 함")
	}
}

// TestListTasks — 태스크 목록 조회 및 필터링 검증
func TestListTasks(t *testing.T) {
	t.Parallel()
	svc := NewTaskService()

	svc.CreateTask("vm.migrate", 1)
	svc.CreateTask("backup.create", 2)
	svc.CreateTask("vm.migrate", 3)

	// 전체 목록
	all := svc.ListTasks("", "")
	if len(all) != 3 {
		t.Errorf("전체 목록 길이: got %d, want 3", len(all))
	}

	// 타입 필터
	migrateOnly := svc.ListTasks("vm.migrate", "")
	if len(migrateOnly) != 2 {
		t.Errorf("vm.migrate 필터 결과: got %d, want 2", len(migrateOnly))
	}

	// 상태 필터
	pendingOnly := svc.ListTasks("", StatusPending)
	if len(pendingOnly) != 3 {
		t.Errorf("pending 필터 결과: got %d, want 3", len(pendingOnly))
	}

	runningOnly := svc.ListTasks("", StatusRunning)
	if len(runningOnly) != 0 {
		t.Errorf("running 필터 결과: got %d, want 0", len(runningOnly))
	}
}

// TestDeleteTask — 완료된 태스크 삭제 검증
func TestDeleteTask(t *testing.T) {
	t.Parallel()
	svc := NewTaskService()

	task := svc.CreateTask("vm.migrate", 1)
	task.Complete()

	if err := svc.DeleteTask(task.ID); err != nil {
		t.Fatalf("DeleteTask() 에러: %v", err)
	}

	_, err := svc.GetTask(task.ID)
	if err == nil {
		t.Fatal("삭제된 태스크가 여전히 조회됨")
	}
}

// TestDeleteTaskNotFound — 존재하지 않는 태스크 삭제 시 에러 반환
func TestDeleteTaskNotFound(t *testing.T) {
	t.Parallel()
	svc := NewTaskService()

	err := svc.DeleteTask("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 태스크 삭제 시 에러가 반환되어야 함")
	}
}

// TestDeleteTaskPending — pending 상태의 태스크 삭제 시 에러 반환
func TestDeleteTaskPending(t *testing.T) {
	t.Parallel()
	svc := NewTaskService()

	task := svc.CreateTask("vm.migrate", 1)

	err := svc.DeleteTask(task.ID)
	if err == nil {
		t.Fatal("pending 태스크 삭제 시 에러가 반환되어야 함")
	}
}

// TestDeleteTaskFailed — 실패한 태스크 삭제 가능 검증
func TestDeleteTaskFailed(t *testing.T) {
	t.Parallel()
	svc := NewTaskService()

	task := svc.CreateTask("vm.migrate", 1)
	task.Fail("migration error")

	if err := svc.DeleteTask(task.ID); err != nil {
		t.Fatalf("DeleteTask() 에러: %v (failed 태스크는 삭제 가능해야 함)", err)
	}
}

// TestRunAsync — 비동기 태스크 실행 후 완료 상태 확인
func TestRunAsync(t *testing.T) {
	t.Parallel()
	svc := NewTaskService()

	task := svc.RunAsync("vm.migrate", 1, func(tk *Task) error {
		tk.SetProgress(50)
		return nil
	})

	// goroutine 완료 대기 (최대 2초)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := task.Snapshot()
		if snap.Status == StatusCompleted {
			if snap.Progress != 100 {
				t.Errorf("완료 후 Progress: got %d, want 100", snap.Progress)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("태스크가 2초 내에 완료되지 않음")
}

// TestRunAsyncFailure — 비동기 태스크 실패 시 상태 확인
func TestRunAsyncFailure(t *testing.T) {
	t.Parallel()
	svc := NewTaskService()

	task := svc.RunAsync("vm.migrate", 1, func(tk *Task) error {
		return fmt.Errorf("migration failed")
	})

	// goroutine 완료 대기 (최대 2초)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := task.Snapshot()
		if snap.Status == StatusFailed {
			if snap.Error != "migration failed" {
				t.Errorf("Error: got %q, want %q", snap.Error, "migration failed")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("태스크가 2초 내에 실패 상태로 전이되지 않음")
}

// TestTaskSetProgress — SetProgress 메서드 검증
func TestTaskSetProgress(t *testing.T) {
	t.Parallel()
	svc := NewTaskService()

	task := svc.CreateTask("test", 0)
	task.SetProgress(42)

	snap := task.Snapshot()
	if snap.Progress != 42 {
		t.Errorf("Progress: got %d, want 42", snap.Progress)
	}
}

// TestTaskSnapshot — Snapshot 메서드가 올바른 복사본을 반환하는지 검증
func TestTaskSnapshot(t *testing.T) {
	t.Parallel()
	svc := NewTaskService()

	task := svc.CreateTask("vm.migrate", 5)
	task.SetStatus(StatusRunning)
	task.SetProgress(75)

	snap := task.Snapshot()
	if snap.Type != "vm.migrate" {
		t.Errorf("Type: got %q, want %q", snap.Type, "vm.migrate")
	}
	if snap.Status != StatusRunning {
		t.Errorf("Status: got %q, want %q", snap.Status, StatusRunning)
	}
	if snap.Progress != 75 {
		t.Errorf("Progress: got %d, want 75", snap.Progress)
	}
	if snap.VMID != 5 {
		t.Errorf("VMID: got %d, want 5", snap.VMID)
	}
}
