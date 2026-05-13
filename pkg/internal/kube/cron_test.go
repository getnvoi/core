package kube_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/core/pkg/internal/kube"
)

// testEmitter collects Progress messages for assertions.
type testEmitter struct {
	mu       sync.Mutex
	messages []string
}

func (e *testEmitter) Progress(msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.messages = append(e.messages, msg)
}

func TestCreateJobFromCronJob_CopiesTemplate(t *testing.T) {
	cron := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pg-backup",
			Namespace: "ns",
			UID:       types.UID("u-1"),
			Labels:    map[string]string{kube.LabelOwner: kube.OwnerDatabases},
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 3 * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app.kubernetes.io/name": "pg"}},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers:    []corev1.Container{{Name: "backup", Image: "nvoi/db:latest"}},
						},
					},
				},
			},
		},
	}
	cs := fake.NewSimpleClientset(cron)
	c := kube.NewForTest(cs)

	if err := c.CreateJobFromCronJob(context.Background(), "ns", "pg-backup", "pg-backup-manual-1"); err != nil {
		t.Fatalf("CreateJobFromCronJob: %v", err)
	}

	got, err := cs.BatchV1().Jobs("ns").Get(context.Background(), "pg-backup-manual-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Annotations["cronjob.kubernetes.io/instantiate"] != "manual" {
		t.Errorf("manual annotation missing")
	}
	if len(got.OwnerReferences) != 1 || got.OwnerReferences[0].UID != "u-1" {
		t.Errorf("owner reference: %v", got.OwnerReferences)
	}
	if got.Spec.Template.Spec.Containers[0].Image != "nvoi/db:latest" {
		t.Errorf("template image not propagated")
	}
}

func TestWaitForJob_Succeeds(t *testing.T) {
	kube.SetJobPollTiming(2*time.Millisecond, time.Second)
	defer kube.SetJobPollTiming(3*time.Second, 5*time.Minute)

	cs := fake.NewSimpleClientset(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-backup-manual-1", Namespace: "ns"},
		Status:     batchv1.JobStatus{Succeeded: 1},
	})
	c := kube.NewForTest(cs)
	emitter := &testEmitter{}

	if err := c.WaitForJob(context.Background(), "ns", "pg-backup-manual-1", emitter); err != nil {
		t.Fatalf("WaitForJob: %v", err)
	}
}

func TestWaitForJob_Failed_ReturnsError(t *testing.T) {
	kube.SetJobPollTiming(2*time.Millisecond, time.Second)
	defer kube.SetJobPollTiming(3*time.Second, 5*time.Minute)

	cs := fake.NewSimpleClientset(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-backup-manual-1", Namespace: "ns"},
		Status:     batchv1.JobStatus{Failed: 1},
	})
	c := kube.NewForTest(cs)

	err := c.WaitForJob(context.Background(), "ns", "pg-backup-manual-1", nil)
	if err == nil {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(err.Error(), "job pg-backup-manual-1 failed") {
		t.Errorf("error message = %q", err.Error())
	}
}

func TestWaitForJob_ImagePullBackOff_FailsFast(t *testing.T) {
	kube.SetJobPollTiming(2*time.Millisecond, time.Second)
	defer kube.SetJobPollTiming(3*time.Second, 5*time.Minute)

	cs := fake.NewSimpleClientset(
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "j", Namespace: "ns"}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "j-abc",
				Namespace: "ns",
				Labels:    map[string]string{"job-name": "j"},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "ImagePullBackOff",
							Message: "no such image",
						},
					},
				}},
			},
		},
	)
	c := kube.NewForTest(cs)

	err := c.WaitForJob(context.Background(), "ns", "j", nil)
	if err == nil || !strings.Contains(err.Error(), "ImagePullBackOff") {
		t.Fatalf("expected ImagePullBackOff fast-fail, got %v", err)
	}
}
