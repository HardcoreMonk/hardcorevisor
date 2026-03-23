// Package auth — SQLite 기반 사용자 저장소 (JWT 인증용)
//
// 아키텍처 위치: auth 패키지 내부, RBAC 미들웨어 및 JWT 서비스와 연동
//   RBAC 미들웨어 → UserDB.VerifyPassword() → bcrypt 해시 비교 → 인증 통과
//   JWT 로그인 핸들러 → UserDB.VerifyPassword() → JWTService.GenerateToken()
//
// UserDB는 SQLite를 사용하여 사용자 정보를 영속적으로 저장한다.
// modernc.org/sqlite (순수 Go 구현)을 사용하므로 CGo가 필요하지 않다.
// 비밀번호는 bcrypt (cost 10)로 해시하여 저장한다.
//
// 데이터베이스 스키마:
//
//	users(id            INTEGER PRIMARY KEY AUTOINCREMENT,
//	      username      TEXT UNIQUE NOT NULL,
//	      password_hash TEXT NOT NULL,
//	      role          TEXT NOT NULL DEFAULT 'viewer',
//	      created_at    DATETIME DEFAULT CURRENT_TIMESTAMP)
//
// 스레드 안전성: database/sql 패키지가 내부적으로 커넥션 풀을 관리하므로 동시 호출에 안전하다.
// WAL 모드를 활성화하여 읽기 동시성을 향상시킨다.
//
// 의존성:
//   - golang.org/x/crypto/bcrypt: 비밀번호 해시
//   - modernc.org/sqlite: 순수 Go SQLite 드라이버
package auth

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/crypto/bcrypt"

	_ "modernc.org/sqlite"
)

// User 는 데이터베이스에 저장된 사용자 레코드를 나타낸다.
//
// 필드:
//   - ID: 자동 증가 기본 키
//   - Username: 고유 사용자 이름 (로그인 식별자)
//   - PasswordHash: bcrypt 해시 (JSON 직렬화 시 제외됨, json:"-")
//   - Role: 역할 ("admin", "operator", "viewer")
//   - CreatedAt: 생성 시각
//
// JSON 직렬화 시 PasswordHash는 제외되므로 API 응답에 해시가 노출되지 않는다.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

// UserDB 는 SQLite 데이터베이스를 래핑하여 사용자 CRUD 및 인증을 제공한다.
//
// 핵심 기능:
//   - CreateUser: bcrypt 해시와 함께 사용자 생성
//   - VerifyPassword: 사용자 이름/비밀번호 검증 (bcrypt 비교)
//   - SeedDefaultAdmin: 빈 DB일 때 기본 admin/admin 계정 생성
//
// 스레드 안전성: database/sql 내부 커넥션 풀에 의해 보호됨
type UserDB struct {
	db *sql.DB
}

// NewUserDB 는 지정된 경로의 SQLite 데이터베이스를 열고 자동 마이그레이션을 수행한다.
//
// 매개변수:
//   - dbPath: SQLite 파일 경로. ":memory:"를 지정하면 인메모리 DB (테스트용)
//
// 처리 순서:
//  1. SQLite 데이터베이스 열기
//  2. WAL(Write-Ahead Logging) 모드 활성화 — 읽기 동시성 향상
//  3. users 테이블 자동 생성 (migrate)
//
// 에러 조건: 파일 접근 실패, WAL 모드 설정 실패, 마이그레이션 실패
// 호출 시점: Controller 초기화 시 (cmd/controller/main.go)
func NewUserDB(dbPath string) (*UserDB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open userdb: %w", err)
	}

	// Enable WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	udb := &UserDB{db: db}
	if err := udb.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate userdb: %w", err)
	}
	return udb, nil
}

// migrate 는 users 테이블이 없으면 생성한다 (CREATE TABLE IF NOT EXISTS).
// NewUserDB에서 자동 호출되므로 외부에서 직접 호출할 필요가 없다.
func (u *UserDB) migrate() error {
	_, err := u.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			username      TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role          TEXT NOT NULL DEFAULT 'viewer',
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

// Close 는 데이터베이스 연결을 닫는다.
// Controller 종료 시 호출하여 리소스를 정리한다.
func (u *UserDB) Close() error {
	return u.db.Close()
}

// CreateUser 는 새 사용자를 bcrypt 해시된 비밀번호와 함께 삽입한다.
//
// 매개변수:
//   - username: 고유 사용자 이름 (중복 시 에러)
//   - password: 평문 비밀번호 (bcrypt cost 10으로 해시하여 저장)
//   - role: 역할 ("admin", "operator", "viewer")
//
// 에러 조건: 사용자 이름 중복 (UNIQUE 제약), bcrypt 해시 실패
// 호출 시점: POST /api/v1/auth/users (admin 전용), SeedDefaultAdmin()
// 부작용: SQLite에 새 행 삽입
func (u *UserDB) CreateUser(username, password, role string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	_, err = u.db.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)",
		username, string(hash), role,
	)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// GetUser 은 사용자 이름으로 사용자를 조회한다.
// 미존재 시 sql.ErrNoRows를 반환한다.
//
// 반환값: User 포인터 (PasswordHash 포함 — JSON 직렬화 시 자동 제외)
// 호출 시점: VerifyPassword에서 내부적으로 호출
func (u *UserDB) GetUser(username string) (*User, error) {
	row := u.db.QueryRow(
		"SELECT id, username, password_hash, role, created_at FROM users WHERE username = ?",
		username,
	)
	var user User
	var createdAt string
	err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &createdAt)
	if err != nil {
		return nil, err
	}
	user.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return &user, nil
}

// ListUsers 는 모든 사용자를 ID 순으로 반환한다.
// 비밀번호 해시는 JSON 직렬화 시 json:"-" 태그에 의해 제외된다.
//
// 호출 시점: GET /api/v1/auth/users (admin 전용)
// 에러 조건: 쿼리 실행 실패, 행 스캔 실패
func (u *UserDB) ListUsers() ([]User, error) {
	rows, err := u.db.Query("SELECT id, username, password_hash, role, created_at FROM users ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var user User
		var createdAt string
		if err := rows.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &createdAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		user.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		users = append(users, user)
	}
	return users, rows.Err()
}

// DeleteUser 는 사용자 이름으로 사용자를 삭제한다.
//
// 에러 조건: 사용자 미존재 (RowsAffected == 0)
// 호출 시점: DELETE /api/v1/auth/users/{username} (admin 전용)
// 부작용: SQLite에서 행 삭제
func (u *UserDB) DeleteUser(username string) error {
	result, err := u.db.Exec("DELETE FROM users WHERE username = ?", username)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("delete user: user %q not found", username)
	}
	return nil
}

// VerifyPassword 는 사용자 이름과 비밀번호를 저장된 bcrypt 해시와 대조하여 검증한다.
//
// 처리 순서:
//  1. GetUser로 사용자 조회 (미존재 시 에러)
//  2. bcrypt.CompareHashAndPassword로 비밀번호 비교 (불일치 시 에러)
//
// 반환값: 인증 성공 시 User 포인터, 실패 시 에러
// 호출 시점: RBAC 미들웨어 (Basic Auth), JWT 로그인 핸들러
// 보안 참고: bcrypt 비교는 타이밍 공격에 안전하다 (상수 시간 비교)
func (u *UserDB) VerifyPassword(username, password string) (*User, error) {
	user, err := u.GetUser(username)
	if err != nil {
		return nil, fmt.Errorf("verify password: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("verify password: invalid credentials")
	}
	return user, nil
}

// SeedDefaultAdmin 은 users 테이블이 비어 있을 때 기본 관리자 계정을 생성한다.
// 생성되는 계정: admin/admin (역할: admin, bcrypt cost 10)
//
// 호출 시점: Controller 초기화 시, NewUserDB 직후에 호출
// 보안 경고: 프로덕션 환경에서는 즉시 비밀번호를 변경해야 한다.
// 멱등성: 이미 사용자가 존재하면 아무 작업도 하지 않는다.
func (u *UserDB) SeedDefaultAdmin() {
	var count int
	if err := u.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		slog.Warn("failed to count users for seeding", "error", err)
		return
	}
	if count > 0 {
		return
	}
	if err := u.CreateUser("admin", "admin", "admin"); err != nil {
		slog.Warn("failed to seed default admin", "error", err)
		return
	}
	slog.Info("seeded default admin user (admin/admin)")
}
