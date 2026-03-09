package main

import (
	"log"
	"os"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/your-org/task-server/internal/activities"
	"github.com/your-org/task-server/internal/workflows"
)

func main() {
	temporalHost := envOr("TEMPORAL_HOST", "temporal-frontend:7233")

	c, err := client.Dial(client.Options{
		HostPort:  temporalHost,
		Namespace: "default",
	})
	if err != nil {
		log.Fatalf("Failed to connect to Temporal: %v", err)
	}
	defer c.Close()

	w := worker.New(c, "conciliation", worker.Options{
		MaxConcurrentActivityExecutionSize:     10,
		MaxConcurrentWorkflowTaskExecutionSize: 10,
	})

	w.RegisterWorkflow(workflows.ConciliationWorkflow)
	w.RegisterActivity(activities.GetPartnerStats)
	w.RegisterActivity(activities.LaunchAndWaitK8sJob)

	log.Println("Task server worker started, listening on queue: conciliation")

	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("Worker failed: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
