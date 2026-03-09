package activities

import (
	"context"
	"fmt"
	"os"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"go.temporal.io/sdk/activity"
)

type ResourceSpec struct {
	CPU    string
	Memory string
	Tier   string
}

type LaunchJobInput struct {
	PartnerCode string
	PartnerID   int
	RecordCount int
	Resources   ResourceSpec
}

type LaunchJobResult struct {
	JobName string
	Status  string
}

func LaunchAndWaitK8sJob(ctx context.Context, input LaunchJobInput) (*LaunchJobResult, error) {
	logger := activity.GetLogger(ctx)

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("k8s config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("k8s client: %w", err)
	}

	jobName := fmt.Sprintf("conciliation-%s-%d", input.PartnerCode, time.Now().Unix())
	namespace := envOr("JOB_NAMESPACE", "temporal-poc")
	workerImage := envOr("CONCILIATION_WORKER_IMAGE", "registry.csepulveda.net/temporal-poc/conciliation-worker:latest")
	databaseURL := os.Getenv("DATABASE_URL")

	job := buildJob(jobName, namespace, workerImage, databaseURL, input)

	created, err := clientset.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create job %s: %w", jobName, err)
	}
	logger.Info("K8s Job created",
		"job", created.Name,
		"partner", input.PartnerCode,
		"tier", input.Resources.Tier,
	)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			activity.RecordHeartbeat(ctx, fmt.Sprintf("waiting for job %s", jobName))

			got, err := clientset.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
			if err != nil {
				return nil, fmt.Errorf("check job status: %w", err)
			}

			if got.Status.Succeeded > 0 {
				logger.Info("K8s Job completed", "job", jobName)
				return &LaunchJobResult{JobName: jobName, Status: "succeeded"}, nil
			}
			if got.Status.Failed > 0 {
				return nil, fmt.Errorf("job %s failed", jobName)
			}
		}
	}
}

func buildJob(name, namespace, image, databaseURL string, input LaunchJobInput) *batchv1.Job {
	backoffLimit := int32(2)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app":        "conciliation-worker",
				"partner":    input.PartnerCode,
				"tier":       input.Resources.Tier,
				"managed-by": "task-server",
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":  "conciliation-worker",
						"tier": input.Resources.Tier,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:            "worker",
							Image:           image,
							ImagePullPolicy: corev1.PullAlways,
							Env: []corev1.EnvVar{
								{Name: "PARTNER_CODE", Value: input.PartnerCode},
								{Name: "PARTNER_ID", Value: fmt.Sprintf("%d", input.PartnerID)},
								{Name: "RECORD_COUNT", Value: fmt.Sprintf("%d", input.RecordCount)},
								{Name: "TIER", Value: input.Resources.Tier},
								{Name: "DATABASE_URL", Value: databaseURL},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(input.Resources.CPU),
									corev1.ResourceMemory: resource.MustParse(input.Resources.Memory),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(input.Resources.CPU),
									corev1.ResourceMemory: resource.MustParse(input.Resources.Memory),
								},
							},
						},
					},
				},
			},
		},
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
