// Leader election — etcd 기반 리더 선출 또는 단일 노드 폴백
//
// etcd가 사용 가능한 경우 etcd concurrency를 사용하여 리더를 선출한다.
// etcd가 없는 경우 단일 노드 모드로 자동 전환되어 자기 자신이 리더가 된다.
//
// 사용:
//
//	le, _ := NewLeaderElection(endpoints, "node-01", 10)
//	le.Campaign(ctx)  // 리더 선출 시작
//	le.IsLeader()     // 리더 여부 확인
//	le.Resign()       // 리더 사임
package ha

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

// LeaderElection 은 etcd 기반 리더 선출을 관리한다.
// etcd가 없는 환경에서는 단일 노드 모드로 동작하여 자기 자신이 리더가 된다.
type LeaderElection struct {
	mu         sync.Mutex
	client     *clientv3.Client
	session    *concurrency.Session
	election   *concurrency.Election
	leaderKey  string
	nodeName   string
	sessionTTL int
	isLeader   atomic.Bool
	cancel     context.CancelFunc
	singleNode bool // true if no etcd connection
}

// NewLeaderElection 은 리더 선출 관리자를 생성한다.
// endpoints가 비어 있거나 etcd 연결에 실패하면 단일 노드 모드로 전환된다.
//
// 매개변수:
//   - endpoints: etcd 엔드포인트 목록 (비어 있으면 단일 노드 모드)
//   - nodeName: 현재 노드 이름
//   - ttl: 세션 TTL (초)
func NewLeaderElection(endpoints []string, nodeName string, ttl int) (*LeaderElection, error) {
	le := &LeaderElection{
		leaderKey:  "/hcv/leader",
		nodeName:   nodeName,
		sessionTTL: ttl,
	}

	if len(endpoints) == 0 {
		slog.Warn("no etcd endpoints for leader election, defaulting to single-node mode",
			"node", nodeName)
		le.singleNode = true
		le.isLeader.Store(true)
		return le, nil
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		slog.Warn("failed to connect to etcd for leader election, defaulting to single-node mode",
			"node", nodeName, "error", err)
		le.singleNode = true
		le.isLeader.Store(true)
		return le, nil
	}

	// Quick health check
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = client.Status(ctx, endpoints[0])
	if err != nil {
		client.Close()
		slog.Warn("etcd health check failed for leader election, defaulting to single-node mode",
			"node", nodeName, "error", err)
		le.singleNode = true
		le.isLeader.Store(true)
		return le, nil
	}

	le.client = client
	return le, nil
}

// Campaign 은 리더 선출을 시작한다.
// etcd 모드에서는 고루틴에서 리더 선출 캠페인을 실행한다.
// 단일 노드 모드에서는 즉시 리더가 된다.
func (le *LeaderElection) Campaign(ctx context.Context) error {
	if le.singleNode {
		le.isLeader.Store(true)
		slog.Info("single-node mode: self is leader", "node", le.nodeName)
		return nil
	}

	le.mu.Lock()
	defer le.mu.Unlock()

	session, err := concurrency.NewSession(le.client, concurrency.WithTTL(le.sessionTTL))
	if err != nil {
		slog.Warn("failed to create etcd session, defaulting to self-as-leader",
			"node", le.nodeName, "error", err)
		le.singleNode = true
		le.isLeader.Store(true)
		return nil
	}
	le.session = session
	le.election = concurrency.NewElection(session, le.leaderKey)

	campaignCtx, cancel := context.WithCancel(ctx)
	le.cancel = cancel

	go func() {
		slog.Info("campaigning for leadership", "node", le.nodeName)
		if err := le.election.Campaign(campaignCtx, le.nodeName); err != nil {
			if campaignCtx.Err() != nil {
				// Context cancelled, normal shutdown
				return
			}
			slog.Warn("leader campaign failed", "node", le.nodeName, "error", err)
			return
		}
		le.isLeader.Store(true)
		slog.Info("elected as leader", "node", le.nodeName)
	}()

	return nil
}

// Resign 은 리더십을 사임한다.
func (le *LeaderElection) Resign() error {
	le.isLeader.Store(false)

	if le.singleNode {
		return nil
	}

	le.mu.Lock()
	defer le.mu.Unlock()

	if le.cancel != nil {
		le.cancel()
	}

	if le.election != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := le.election.Resign(ctx); err != nil {
			return fmt.Errorf("resign leadership: %w", err)
		}
	}

	if le.session != nil {
		le.session.Close()
	}

	return nil
}

// IsLeader 는 현재 노드가 리더인지 반환한다.
func (le *LeaderElection) IsLeader() bool {
	return le.isLeader.Load()
}

// GetLeader 는 현재 리더의 이름을 반환한다.
func (le *LeaderElection) GetLeader() (string, error) {
	if le.singleNode {
		return le.nodeName, nil
	}

	if le.election == nil {
		return le.nodeName, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := le.election.Leader(ctx)
	if err != nil {
		// Fallback: if we know we are leader, return self
		if le.isLeader.Load() {
			return le.nodeName, nil
		}
		return "", fmt.Errorf("get leader: %w", err)
	}

	if len(resp.Kvs) > 0 {
		return string(resp.Kvs[0].Value), nil
	}
	return le.nodeName, nil
}

// Close 은 리더 선출 자원을 정리한다.
func (le *LeaderElection) Close() error {
	if err := le.Resign(); err != nil {
		slog.Warn("error during leader election close", "error", err)
	}
	if le.client != nil {
		return le.client.Close()
	}
	return nil
}
