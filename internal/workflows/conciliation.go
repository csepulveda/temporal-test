package workflows

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/your-org/task-server/internal/activities"
)

type ConciliationInput struct {
	PartnerCode string `json:"partner_code"`
	Source      string `json:"source"`
}

type ConciliationResult struct {
	PartnerCode       string
	MerchantsWithLoan int
	PendingTxns       int
	Tier              string
	JobName           string
	Status            string
}

func ConciliationWorkflow(ctx workflow.Context, input ConciliationInput) (*ConciliationResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting conciliation", "partner", input.PartnerCode, "source", input.Source)

	// Step 1: Evaluate workload
	quickCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})

	var stats activities.PartnerStats
	err := workflow.ExecuteActivity(quickCtx, activities.GetPartnerStats, input.PartnerCode).Get(ctx, &stats)
	if err != nil {
		return nil, fmt.Errorf("get partner stats: %w", err)
	}

	logger.Info("Partner evaluated",
		"partner", input.PartnerCode,
		"merchants", stats.MerchantCount,
		"active_loans", stats.ActiveLoans,
		"pending_txns", stats.PendingTransactions,
	)

	if stats.ActiveLoans == 0 {
		return &ConciliationResult{
			PartnerCode: input.PartnerCode,
			Status:      "skipped_no_loans",
		}, nil
	}

	// Step 2: Calculate resources
	resources := calculateResources(stats.PendingTransactions)
	logger.Info("Resources calculated", "tier", resources.Tier, "cpu", resources.CPU, "memory", resources.Memory)

	// Step 3: Launch K8s Job
	jobCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 4 * time.Hour,
		HeartbeatTimeout:    5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})

	jobInput := activities.LaunchJobInput{
		PartnerCode: input.PartnerCode,
		PartnerID:   stats.PartnerID,
		RecordCount: stats.PendingTransactions,
		Resources:   resources,
	}

	var jobResult activities.LaunchJobResult
	err = workflow.ExecuteActivity(jobCtx, activities.LaunchAndWaitK8sJob, jobInput).Get(ctx, &jobResult)
	if err != nil {
		return nil, fmt.Errorf("conciliation job failed: %w", err)
	}

	return &ConciliationResult{
		PartnerCode:       input.PartnerCode,
		MerchantsWithLoan: stats.ActiveLoans,
		PendingTxns:       stats.PendingTransactions,
		Tier:              resources.Tier,
		JobName:           jobResult.JobName,
		Status:            jobResult.Status,
	}, nil
}

func calculateResources(pendingTxns int) activities.ResourceSpec {
	switch {
	case pendingTxns < 1_000:
		return activities.ResourceSpec{CPU: "250m", Memory: "256Mi", Tier: "light"}
	case pendingTxns < 50_000:
		return activities.ResourceSpec{CPU: "1", Memory: "1Gi", Tier: "medium"}
	case pendingTxns < 500_000:
		return activities.ResourceSpec{CPU: "2", Memory: "4Gi", Tier: "heavy"}
	default:
		return activities.ResourceSpec{CPU: "4", Memory: "8Gi", Tier: "extra-heavy"}
	}
}
