package diag

import (
	"context"
	"fmt"
	"testing"
)

func TestCheckExecAccess(t *testing.T) {
	ctx := context.Background()
	pod := PodRef{Namespace: "kube-system", Name: "ovn-central-0", Container: "ovn-central"}

	// Success case
	mock := NewMockExecutor()
	mock.StdoutMap["true"] = []byte("")
	allowed, reason, err := CheckExecAccess(ctx, mock, pod)
	if err != nil || !allowed {
		t.Fatalf("expected allowed=true, got %v (reason: %s, err: %v)", allowed, reason, err)
	}

	// RBAC Denied case
	mockDenied := NewMockExecutor()
	mockDenied.ErrMap["true"] = fmt.Errorf("pods \"ovn-central-0\" is forbidden: User cannot create resource \"pods/exec\"")
	allowed, reason, err = CheckExecAccess(ctx, mockDenied, pod)
	if err != nil {
		t.Fatalf("unexpected error for RBAC denied case: %v", err)
	}
	if allowed {
		t.Fatalf("expected allowed=false for forbidden probe")
	}
	if reason == "" {
		t.Errorf("expected non-empty reason for denial")
	}
}
