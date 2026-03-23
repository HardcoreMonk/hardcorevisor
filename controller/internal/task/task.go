// Package task — 비동기 태스크 큐 서비스
//
// 장시간 작업(마이그레이션, 백업 생성, 스냅샷 등)을 비동기로 실행하고
// 진행 상태를 추적할 수 있는 태스크 큐를 제공한다.
//
// # 아키텍처 위치
//
//	API 핸들러 → TaskService.RunAsync() → goroutine (fn 실행)
//	                                        ↓
//	API 핸들러 → TaskService.GetTask()   ← 상태 폴링
//
// # 태스크 상태 흐름
//
//	pending → running → completed
//	                  → failed
//
// 스레드 안전성: 내부 sync.RWMutex로 보호됨
package task

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// 태스크 상태 상수
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// TaskSnapshot — 태스크의 읽기 전용 스냅샷. JSON 직렬화에 안전하다.
type TaskSnapshot struct {
	ID          string      `json:"id"`
	Type        string      `json:"type"`
	Status      string      `json:"status"`
	Progress    int         `json:"progress"`
	Result      interface{} `json:"result,omitempty"`
	Error       string      `json:"error,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	CompletedAt time.Time   `json:"completed_at,omitempty"`
	VMID        int32       `json:"vm_id,omitempty"`
}

// Task — 비동기 작업의 상태를 나타낸다.
type Task struct {
	mu          sync.Mutex
	ID          string
	Type        string
	Status      string
	Progress    int
	Result      interface{}
	Error       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt time.Time
	VMID        int32
}

// SetProgress — 태스크의 진행률과 상태를 안전하게 업데이트한다.
func (t *Task) SetProgress(progress int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Progress = progress
	t.UpdatedAt = time.Now()
}

// SetStatus — 태스크의 상태를 안전하게 업데이트한다.
func (t *Task) SetStatus(status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = status
	t.UpdatedAt = time.Now()
}

// Complete — 태스크를 성공 완료 상태로 설정한다.
func (t *Task) Complete() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = StatusCompleted
	t.Progress = 100
	t.CompletedAt = time.Now()
	t.UpdatedAt = time.Now()
}

// Fail — 태스크를 실패 상태로 설정한다.
func (t *Task) Fail(errMsg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = StatusFailed
	t.Error = errMsg
	t.CompletedAt = time.Now()
	t.UpdatedAt = time.Now()
}

// Snapshot — 태스크의 현재 상태를 복사한 읽기 전용 스냅샷을 반환한다.
// JSON 직렬화 시 race condition을 방지하기 위해 사용한다.
func (t *Task) Snapshot() TaskSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TaskSnapshot{
		ID:          t.ID,
		Type:        t.Type,
		Status:      t.Status,
		Progress:    t.Progress,
		Result:      t.Result,
		Error:       t.Error,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
		CompletedAt: t.CompletedAt,
		VMID:        t.VMID,
	}
}

// TaskService — 비동기 태스크 관리 서비스.
// 태스크 생성, 조회, 삭제 및 비동기 실행을 제공한다.
type TaskService struct {
	mu     sync.RWMutex
	tasks  map[string]*Task
	nextID atomic.Int64
}

// NewTaskService — 새 TaskService를 생성한다.
func NewTaskService() *TaskService {
	return &TaskService{
		tasks: make(map[string]*Task),
	}
}

// CreateTask — 새 pending 태스크를 생성한다.
//
// 매개변수:
//   - taskType: 태스크 유형 (e.g., "vm.migrate", "backup.create")
//   - vmID: 관련 VM ID (0이면 무관)
func (s *TaskService) CreateTask(taskType string, vmID int32) *Task {
	id := fmt.Sprintf("task-%d", s.nextID.Add(1))
	now := time.Now()
	t := &Task{
		ID:        id,
		Type:      taskType,
		Status:    StatusPending,
		Progress:  0,
		CreatedAt: now,
		UpdatedAt: now,
		VMID:      vmID,
	}
	s.mu.Lock()
	s.tasks[id] = t
	s.mu.Unlock()
	return t
}

// UpdateTask — 태스크 상태를 업데이트한다.
func (s *TaskService) UpdateTask(id string, status string, progress int, result interface{}) error {
	s.mu.RLock()
	t, ok := s.tasks[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = status
	t.Progress = progress
	t.UpdatedAt = time.Now()
	if result != nil {
		t.Result = result
	}
	if status == StatusCompleted || status == StatusFailed {
		t.CompletedAt = time.Now()
	}
	return nil
}

// GetTask — ID로 태스크를 조회한다.
func (s *TaskService) GetTask(id string) (*Task, error) {
	s.mu.RLock()
	t, ok := s.tasks[id]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	return t, nil
}

// ListTasks — 태스크 목록을 반환한다. filter로 type 또는 status를 필터링할 수 있다.
func (s *TaskService) ListTasks(typeFilter, statusFilter string) []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		if typeFilter != "" && t.Type != typeFilter {
			continue
		}
		if statusFilter != "" && t.Status != statusFilter {
			continue
		}
		result = append(result, t)
	}
	return result
}

// DeleteTask — 완료/실패한 태스크를 삭제한다.
func (s *TaskService) DeleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	t.mu.Lock()
	status := t.Status
	t.mu.Unlock()
	if status != StatusCompleted && status != StatusFailed {
		return fmt.Errorf("cannot delete task %s in status %s", id, status)
	}
	delete(s.tasks, id)
	return nil
}

// RunAsync — 태스크를 생성하고 fn을 goroutine에서 비동기 실행한다.
// fn 내에서 t.SetProgress(), t.SetStatus()로 진행 상태를 업데이트할 수 있다.
// fn이 nil을 반환하면 completed, 에러를 반환하면 failed 상태가 된다.
func (s *TaskService) RunAsync(taskType string, vmID int32, fn func(t *Task) error) *Task {
	t := s.CreateTask(taskType, vmID)

	go func() {
		t.SetStatus(StatusRunning)
		if err := fn(t); err != nil {
			t.mu.Lock()
			t.Status = StatusFailed
			t.Error = err.Error()
			t.CompletedAt = time.Now()
			t.UpdatedAt = time.Now()
			t.mu.Unlock()
			slog.Warn("async task failed", "task_id", t.ID, "type", t.Type, "error", err)
		} else {
			t.mu.Lock()
			t.Status = StatusCompleted
			t.Progress = 100
			t.CompletedAt = time.Now()
			t.UpdatedAt = time.Now()
			t.mu.Unlock()
			slog.Info("async task completed", "task_id", t.ID, "type", t.Type)
		}
	}()

	return t
}
