package kube

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/internal/utils"
)

// ProgressEmitter receives status updates during long-running polls
// (job-completion waits, rollout waits). Narrow on purpose so the kube
// package doesn't import the log package — the deploy / CLI layer
// wires log.Log into a thin adapter when calling these helpers.
type ProgressEmitter interface {
	Progress(msg string)
}

// jobPollInterval is the interval between Job-readiness polls. Kept
// short so backup completions feel snappy in the CLI; ZFS dumps
// usually take seconds to a minute.
var jobPollInterval = 3 * time.Second

// jobTimeout is the maximum time WaitForJob blocks. Backup jobs on
// a large DB can take several minutes; restore can take longer. Five
// minutes is the rollout default in `../nvoi` and a reasonable upper
// bound for the common case.
var jobTimeout = 5 * time.Minute

// SetJobPollTiming overrides Job poll timing for tests. Same shape as
// SetTestTiming in rollout.go.
func SetJobPollTiming(poll, max time.Duration) {
	jobPollInterval = poll
	jobTimeout = max
}

// CreateJobFromCronJob creates a one-off Job from an existing
// CronJob's template. Equivalent to `kubectl create job --from=
// cronjob/<name>` without shelling out. Used by `nvoi database
// backup now` so manual + scheduled backups share the exact same Job
// spec, image, env, and bucket creds — no drift between the two
// paths.
func (c *Client) CreateJobFromCronJob(ctx context.Context, ns, cronName, jobName string) error {
	cron, err := c.CS.BatchV1().CronJobs(ns).Get(ctx, cronName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get cronjob/%s: %w", cronName, err)
	}
	annotations := map[string]string{"cronjob.kubernetes.io/instantiate": "manual"}
	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        jobName,
			Namespace:   ns,
			Labels:      cron.Spec.JobTemplate.Labels,
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "batch/v1",
				Kind:       "CronJob",
				Name:       cron.Name,
				UID:        cron.UID,
			}},
		},
		Spec: cron.Spec.JobTemplate.Spec,
	}
	if _, err := c.CS.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{FieldManager: FieldManager}); err != nil {
		return fmt.Errorf("create job/%s from cronjob/%s: %w", jobName, cronName, err)
	}
	return nil
}

// WaitForJob polls a Job until it succeeds or fails. Detects terminal
// failures (CrashLoopBackOff, BackOff, OOMKilled, ImagePullBackOff,
// CreateContainerConfigError) immediately and returns the container
// logs on failure. Used by `backup now` (so the CLI doesn't return
// success while the dump is still running) and by `migrate` (which
// composes Backup→teardown→Apply→Restore and needs each step to
// complete before the next).
//
// emitter may be nil — progress events are dropped silently in that
// case. Callers wiring a Log adapter should use AsEmitter (below).
func (c *Client) WaitForJob(ctx context.Context, ns, jobName string, emitter ProgressEmitter) error {
	selector := fmt.Sprintf("job-name=%s", jobName)
	lastStatus := ""

	return utils.Poll(ctx, jobPollInterval, jobTimeout, func() (bool, error) {
		job, err := c.CS.BatchV1().Jobs(ns).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return false, nil // transient — keep polling
		}
		if job != nil {
			if job.Status.Succeeded > 0 {
				return true, nil
			}
			if job.Status.Failed > 0 {
				logs := c.recentJobLogs(ctx, ns, jobName, 30)
				return false, fmt.Errorf("job %s failed\nlogs:\n%s", jobName, indent(logs, "  "))
			}
		}

		pods, err := c.CS.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return false, nil
		}

		for _, pod := range pods.Items {
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil {
					reason := cs.State.Waiting.Reason
					switch reason {
					case "CrashLoopBackOff", "BackOff":
						logs := c.podLogs(ctx, ns, pod.Name, &corev1.PodLogOptions{TailLines: int64Ptr(30)})
						return false, fmt.Errorf("job %s: %s\nlogs:\n%s", jobName, reason, indent(logs, "  "))
					case "ImagePullBackOff", "ErrImagePull":
						return false, fmt.Errorf("job %s: %s — %s", jobName, reason, cs.State.Waiting.Message)
					case "CreateContainerConfigError":
						return false, fmt.Errorf("job %s: %s — %s", jobName, reason, cs.State.Waiting.Message)
					}
				}
				if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
					return false, fmt.Errorf("job %s: OOMKilled", jobName)
				}
			}
		}

		status := fmt.Sprintf("job %s running", jobName)
		if status != lastStatus {
			if emitter != nil {
				emitter.Progress(status)
			}
			lastStatus = status
		}
		return false, nil
	})
}

// recentJobLogs fetches the last `tail` lines from the Job's first
// pod. Tries previous-container first — on a crash loop the useful
// logs (the actual crash) live in the previous container instance,
// not the current one which just started.
func (c *Client) recentJobLogs(ctx context.Context, ns, jobName string, tail int) string {
	pods, err := c.CS.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", jobName),
	})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}
	podName := pods.Items[0].Name
	if prev := c.podLogs(ctx, ns, podName, &corev1.PodLogOptions{
		Previous:  true,
		TailLines: int64Ptr(int64(tail)),
	}); prev != "" {
		return prev
	}
	return c.podLogs(ctx, ns, podName, &corev1.PodLogOptions{
		TailLines: int64Ptr(int64(tail)),
	})
}

// podLogs reads stream-bound pod logs into a string. Returns empty
// string on any error — log fetch is best-effort diagnostics, not a
// load-bearing read.
func (c *Client) podLogs(ctx context.Context, ns, podName string, opts *corev1.PodLogOptions) string {
	req := c.CS.CoreV1().Pods(ns).GetLogs(podName, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return ""
	}
	defer stream.Close()
	data, err := io.ReadAll(stream)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func int64Ptr(v int64) *int64 { return &v }

// indent prefixes every line of s with prefix. Used for error
// messages that embed pod logs — the indent makes the boundary
// between nvoi's error string and the pod's stdout visually clear.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
