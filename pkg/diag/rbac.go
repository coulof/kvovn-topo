package diag

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// CheckExecAccess probes whether the current operator credentials have pods/exec permissions
// by running a no-op probe command ("true") against the discovered target pod.
func CheckExecAccess(ctx context.Context, exec Executor, targetPod PodRef) (bool, string, error) {
	if targetPod.Name == "" {
		return false, "Target pod not found", fmt.Errorf("empty target pod")
	}

	_, _, err := exec.Exec(ctx, targetPod, []string{"true"})
	if err == nil {
		return true, "Authorized for pods/exec", nil
	}

	errStr := strings.ToLower(err.Error())
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) ||
		strings.Contains(errStr, "forbidden") || strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "cannot exec into") {
		return false, fmt.Sprintf("RBAC denied pods/exec in namespace '%s': %v", targetPod.Namespace, err), nil
	}

	return false, "", fmt.Errorf("probe failed: %w", err)
}
