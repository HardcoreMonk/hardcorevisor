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
// API 레이어(router.go)에서 REST 엔드포인트로 태스크 CRUD를 노출한다:
//   - GET    /api/v1/tasks       → handleListTasks  (목록 조회, type/status 필터)
//   - GET    /api/v1/tasks/{id}  → handleGetTask    (단건 조회)
//   - DELETE /api/v1/tasks/{id}  → handleDeleteTask (완료/실패 태스크 삭제)
//
// # 태스크 상태 흐름
//
//	pending → running → completed
//	                  → failed
//
// # 사용 예시 (마이그레이션)
//
//	t := taskSvc.RunAsync("vm.migrate", vmID, func(t *Task) error {
//	    t.SetProgress(10)
//	    // ... 실제 마이그레이션 로직 ...
//	    t.SetProgress(100)
//	    return nil  // nil → completed, error → failed
//	})
//
// 스레드 안전성: TaskService 내부 sync.RWMutex, Task 내부 sync.Mutex로 보호됨.
// 여러 goroutine에서 동시에 접근해도 안전하다.
//
// 의존성:
//   - log/slog: 태스크 완료/실패 시 구조화 로깅
//   - sync/atomic: 태스크 ID 순차 생성 (lock-free)
package task

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// 태스크 상태 상수.
// 상태 전이: StatusPending → StatusRunning → StatusCompleted 또는 StatusFailed.
// 역방향 전이는 허용되지 않는다.
const (
	StatusPending   = "pending"   // 태스크 생성됨, 아직 실행 시작 전
	StatusRunning   = "running"   // goroutine에서 실행 중
	StatusCompleted = "completed" // 성공적으로 완료됨 (Progress=100)
	StatusFailed    = "failed"    // 에러 발생으로 실패함 (Error 필드에 원인 기록)
)

// TaskSnapshot — 태스크의 읽기 전용 스냅샷 (값 복사본).
// Task.Snapshot()으로 생성되며, JSON 직렬화 시 race condition을 방지한다.
// API 응답(handleListTasks, handleGetTask)에서 직접 직렬화된다.
//
// 필드:
//   - ID: 태스크 고유 식별자 (형식: "task-{순번}", 예: "task-1")
//   - Type: 태스크 유형 (예: "vm.migrate", "backup.create")
//   - Status: 현재 상태 (StatusPending/Running/Completed/Failed)
//   - Progress: 진행률 (0~100, 완료 시 100)
//   - Result: 태스크 결과 데이터 (선택, 태스크 유형에 따라 다름)
//   - Error: 실패 시 에러 메시지 (성공 시 빈 문자열)
//   - CreatedAt: 태스크 생성 시각
//   - UpdatedAt: 마지막 상태 변경 시각
//   - CompletedAt: 완료/실패 시각 (진행 중이면 zero value)
//   - VMID: 관련 VM ID (VM 무관 태스크는 0)
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
//
// 내부 sync.Mutex(mu)로 필드 접근을 보호한다.
// 외부에서 필드를 직접 읽지 말고, 반드시 Snapshot()을 사용하거나
// SetProgress(), SetStatus(), Complete(), Fail() 메서드로 변경해야 한다.
//
// RunAsync()에서 생성되며, goroutine 내부의 fn 콜백에서 진행 상태를 업데이트한다.
// fn이 완료되면 자동으로 completed/failed 상태로 전이된다.
type Task struct {
	mu          sync.Mutex  // 필드 접근 보호용 뮤텍스
	ID          string      // 태스크 고유 ID (예: "task-1")
	Type        string      // 태스크 유형 (예: "vm.migrate")
	Status      string      // 현재 상태 (StatusPending/Running/Completed/Failed)
	Progress    int         // 진행률 (0~100)
	Result      interface{} // 결과 데이터 (선택)
	Error       string      // 실패 시 에러 메시지
	CreatedAt   time.Time   // 생성 시각
	UpdatedAt   time.Time   // 마지막 상태 변경 시각
	CompletedAt time.Time   // 완료/실패 시각
	VMID        int32       // 관련 VM ID (0이면 무관)
}

// SetProgress — 태스크의 진행률을 안전하게 업데이트한다.
//
// 매개변수:
//   - progress: 진행률 (0~100). 호출 측에서 범위를 관리해야 한다.
//
// 부작용: UpdatedAt을 현재 시각으로 갱신
// 스레드 안전성: 안전 (mu.Lock)
// 호출 시점: RunAsync의 fn 콜백 내부에서 진행 상황 보고 시
func (t *Task) SetProgress(progress int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Progress = progress
	t.UpdatedAt = time.Now()
}

// SetStatus — 태스크의 상태를 안전하게 업데이트한다.
//
// 매개변수:
//   - status: 새 상태 (StatusPending/Running/Completed/Failed)
//
// 부작용: UpdatedAt을 현재 시각으로 갱신
// 스레드 안전성: 안전 (mu.Lock)
// 호출 시점: RunAsync 내부에서 running 전이 시, 또는 fn 콜백에서 상태 변경 시
func (t *Task) SetStatus(status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = status
	t.UpdatedAt = time.Now()
}

// Complete — 태스크를 성공 완료 상태로 설정한다.
//
// 처리: Status=completed, Progress=100, CompletedAt=now, UpdatedAt=now
// 스레드 안전성: 안전 (mu.Lock)
// 호출 시점: fn 콜백 완료 후 RunAsync 내부에서 자동 호출, 또는 외부 감시 goroutine에서 호출
func (t *Task) Complete() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = StatusCompleted
	t.Progress = 100
	t.CompletedAt = time.Now()
	t.UpdatedAt = time.Now()
}

// Fail — 태스크를 실패 상태로 설정한다.
//
// 매개변수:
//   - errMsg: 실패 원인 에러 메시지 (Error 필드에 저장)
//
// 처리: Status=failed, Error=errMsg, CompletedAt=now, UpdatedAt=now
// 스레드 안전성: 안전 (mu.Lock)
// 호출 시점: fn 콜백 에러 반환 시 RunAsync 내부에서 자동 호출, 또는 외부 감시 goroutine에서 호출
func (t *Task) Fail(errMsg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = StatusFailed
	t.Error = errMsg
	t.CompletedAt = time.Now()
	t.UpdatedAt = time.Now()
}

// Snapshot — 태스크의 현재 상태를 복사한 읽기 전용 스냅샷(값 타입)을 반환한다.
//
// JSON 직렬화 시 race condition을 방지하기 위해 사용한다.
// 반환된 TaskSnapshot은 원본 Task와 독립적이므로 안전하게 직렬화할 수 있다.
//
// 반환값: TaskSnapshot (값 타입, 포인터 아님)
// 스레드 안전성: 안전 (mu.Lock으로 전체 필드를 원자적으로 복사)
// 호출 시점: handleListTasks, handleGetTask에서 JSON 응답 생성 시
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
//
// 필드:
//   - mu: 태스크 맵(tasks) 접근 보호용 RWMutex
//   - tasks: 태스크 ID → Task 포인터 맵 (인메모리 저장)
//   - nextID: 태스크 ID 순차 카운터 (atomic, lock-free)
//
// 스레드 안전성: 안전. 태스크 맵은 mu(RWMutex)로, 개별 태스크는 Task.mu(Mutex)로 보호.
// 주의: 인메모리이므로 Controller 재시작 시 모든 태스크 기록이 초기화된다.
//
// 호출 시점: Controller 초기화 시 (cmd/controller/main.go)에서 NewTaskService()로 생성,
// Services 구조체에 주입하여 API 핸들러에서 사용.
type TaskService struct {
	mu     sync.RWMutex
	tasks  map[string]*Task
	nextID atomic.Int64
}

// NewTaskService — 새 TaskService를 생성한다.
//
// 빈 태스크 맵으로 초기화한다. nextID는 atomic.Int64의 zero value(0)에서 시작,
// 첫 번째 태스크 ID는 "task-1"이 된다.
//
// 호출 시점: Controller 초기화 시 (cmd/controller/main.go)
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

// UpdateTask — 태스크 상태를 외부에서 직접 업데이트한다.
//
// 매개변수:
//   - id: 태스크 ID (예: "task-1")
//   - status: 새 상태 (StatusPending/Running/Completed/Failed)
//   - progress: 진행률 (0~100)
//   - result: 결과 데이터 (nil이면 기존 값 유지)
//
// status가 completed 또는 failed이면 CompletedAt도 함께 설정된다.
//
// 에러 조건: 태스크 ID가 존재하지 않는 경우
// 스레드 안전성: 안전 (RLock으로 맵 조회, Task.mu.Lock으로 필드 변경)
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
//
// 매개변수:
//   - id: 태스크 ID (예: "task-1")
//
// 반환값: Task 포인터. 필드를 직접 읽지 말고 Snapshot()을 사용할 것.
// 에러 조건: 태스크 ID가 존재하지 않는 경우
// 호출 시점: REST GET /api/v1/tasks/{id} (handleGetTask)
func (s *TaskService) GetTask(id string) (*Task, error) {
	s.mu.RLock()
	t, ok := s.tasks[id]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	return t, nil
}

// ListTasks — 태스크 목록을 반환한다.
//
// 매개변수:
//   - typeFilter: 태스크 유형 필터 (빈 문자열이면 전체, 예: "vm.migrate")
//   - statusFilter: 상태 필터 (빈 문자열이면 전체, 예: "running")
//
// 반환값: 필터 조건에 맞는 Task 포인터 슬라이스
// 호출 시점: REST GET /api/v1/tasks?type=&status= (handleListTasks)
// 스레드 안전성: 안전 (RLock). 반환된 슬라이스의 개별 Task 접근 시 Snapshot() 사용 권장.
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
//
// 매개변수:
//   - id: 삭제할 태스크 ID (예: "task-1")
//
// 에러 조건:
//   - 태스크 ID가 존재하지 않는 경우
//   - 태스크가 아직 pending 또는 running 상태인 경우 (진행 중 삭제 불가)
//
// 부작용: tasks 맵에서 해당 태스크 엔트리 제거
// 호출 시점: REST DELETE /api/v1/tasks/{id} (handleDeleteTask)
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
//
// 처리 순서:
//  1. CreateTask로 pending 태스크 생성
//  2. goroutine 시작: status를 running으로 전이
//  3. fn(t) 실행 — fn 내에서 t.SetProgress(), t.SetStatus()로 진행 상태 보고
//  4. fn이 nil 반환 → completed (Progress=100), 에러 반환 → failed (Error 설정)
//
// 매개변수:
//   - taskType: 태스크 유형 (예: "vm.migrate", "backup.create")
//   - vmID: 관련 VM ID (0이면 무관)
//   - fn: 비동기 실행할 함수. Task 포인터를 받아 진행 상태를 업데이트한다.
//
// 반환값: 생성된 Task 포인터 (즉시 반환, goroutine은 백그라운드 실행)
// 부작용: goroutine 생성. 완료/실패 시 slog로 구조화 로깅.
// 호출 시점: handleMigrateVM에서 비동기 마이그레이션 태스크 생성 시
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
