package kube

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/internal/utils"
)

// rolloutPollInterval is the interval between StatefulSet readiness
// polls. Same default as the cron poll — tight enough for snappy
// rollback feedback, slow enough not to hammer the apiserver.
var rolloutPollInterval = 3 * time.Second

// rolloutTimeout is the maximum time WaitForStatefulSetReady blocks.
// Postgres pods on first boot run initdb (a few seconds on stock
// hardware); subsequent boots are ZFS-mount-and-go (sub-second).
// Five minutes is the upper bound that catches "ImagePullBackOff
// chewing through retries" without making the CLI feel hung.
var rolloutTimeout = 5 * time.Minute

// SetRolloutPollTiming overrides rollout poll timing for tests.
func SetRolloutPollTiming(poll, max time.Duration) {
	rolloutPollInterval = poll
	rolloutTimeout = max
}

// WaitForStatefulSetReady polls until the named StatefulSet has all
// declared replicas in Ready state. Fails fast on unrecoverable pod
// states (ImagePullBackOff, OOMKilled, Unschedulable,
// CreateContainerConfigError); keeps polling through transient
// states (CrashLoopBackOff, probe failure) until the outer timeout.
//
// Used by `nvoi database migrate` to wait for the new node's pod
// to come up before triggering the restore Job. emitter may be nil
// — progress events drop silently.
func (c *Client) WaitForStatefulSetReady(ctx context.Context, ns, name string, emitter ProgressEmitter) error {
	lastStatus := ""

	err := utils.Poll(ctx, rolloutPollInterval, rolloutTimeout, func() (bool, error) {
		ss, err := c.CS.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, nil
		}

		// Replicas pointer can be nil during early reconcile — treat
		// "nil or 0" as "wait until the controller writes it".
		desired := int32(0)
		if ss.Spec.Replicas != nil {
			desired = *ss.Spec.Replicas
		}
		ready := ss.Status.ReadyReplicas

		// Probe per-pod state for fail-fast diagnostics. List by the
		// StatefulSet's selector — same path the controller uses.
		selector := metav1.FormatLabelSelector(ss.Spec.Selector)
		pods, perr := c.CS.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if perr == nil {
			for _, pod := range pods.Items {
				if reason := hardFailReason(&pod); reason != "" {
					logs := c.podLogs(ctx, ns, pod.Name, &corev1.PodLogOptions{TailLines: int64Ptr(30)})
					if logs != "" {
						return false, fmt.Errorf("statefulset %s/%s: %s\nlogs:\n%s", ns, name, reason, indent(logs, "  "))
					}
					return false, fmt.Errorf("statefulset %s/%s: %s", ns, name, reason)
				}
			}
		}

		status := fmt.Sprintf("statefulset %s/%s: %d/%d ready", ns, name, ready, desired)
		if status != lastStatus {
			if emitter != nil {
				emitter.Progress(status)
			}
			lastStatus = status
		}

		return desired > 0 && ready >= desired && ss.Status.ObservedGeneration >= ss.Generation, nil
	})
	if err != nil {
		return fmt.Errorf("statefulset %s/%s not ready: %w", ns, name, err)
	}
	return nil
}

// hardFailReason returns a non-empty reason when a container state is
// genuinely unrecoverable and further polling is pointless. Same
// closed set as ../nvoi: image can't be fetched, config is malformed,
// scheduler can't place the pod, container keeps being OOM-killed.
// CrashLoopBackOff / plain Error exits / probe failing are treated as
// transient (the controller will converge or the timeout will bail).
func hardFailReason(pod *corev1.Pod) string {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == "Unschedulable" {
			return fmt.Sprintf("pod %s unschedulable — %s", pod.Name, cond.Message)
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "ErrImageNeverPull":
				return fmt.Sprintf("%s — %s", cs.State.Waiting.Reason, cs.State.Waiting.Message)
			case "CreateContainerConfigError":
				return fmt.Sprintf("%s — %s", cs.State.Waiting.Reason, cs.State.Waiting.Message)
			}
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
			return "OOMKilled — container ran out of memory"
		}
	}
	return ""
}
