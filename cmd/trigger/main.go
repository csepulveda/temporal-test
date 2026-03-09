package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	_ "github.com/lib/pq"
	"go.temporal.io/sdk/client"
)

type ConciliationInput struct {
	PartnerCode string `json:"partner_code"`
	Source      string `json:"source"`
}

// SNSEnvelope is the wrapper SNS adds when publishing to SQS.
type SNSEnvelope struct {
	Type    string `json:"Type"`
	Message string `json:"Message"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	temporalHost := envOr("TEMPORAL_HOST", "temporal-frontend:7233")

	tc, err := client.Dial(client.Options{
		HostPort:  temporalHost,
		Namespace: "default",
	})
	if err != nil {
		log.Fatalf("Failed to connect to Temporal: %v", err)
	}
	defer tc.Close()

	sqsClient := newSQSClient(ctx)

	go listenSQS(ctx, tc, sqsClient, os.Getenv("CONCILIATION_QUEUE_URL"))
	go startAPI(tc)

	log.Println("Trigger service started: SQS listener + API")
	<-ctx.Done()
	log.Println("Shutting down...")
}

func newSQSClient(ctx context.Context) *sqs.Client {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("Failed to load AWS config: %v", err)
	}
	if endpoint := os.Getenv("AWS_ENDPOINT_URL"); endpoint != "" {
		return sqs.NewFromConfig(cfg, func(o *sqs.Options) {
			o.BaseEndpoint = &endpoint
		})
	}
	return sqs.NewFromConfig(cfg)
}

// ============================================================
// SQS Listener — picks up messages from SNS→SQS or direct SQS
// ============================================================

func listenSQS(ctx context.Context, tc client.Client, sqsClient *sqs.Client, queueURL string) {
	if queueURL == "" {
		log.Println("CONCILIATION_QUEUE_URL not set, SQS listener disabled")
		return
	}

	log.Printf("Listening SQS: %s", queueURL)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		output, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            &queueURL,
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     20,
		})
		if err != nil {
			log.Printf("SQS error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for _, msg := range output.Messages {
			partnerCode, source := parseMessage(*msg.Body)
			if partnerCode == "" {
				log.Printf("Could not extract partner_code from message: %s", *msg.Body)
				continue
			}

			workflowID := fmt.Sprintf("conciliation-%s-%d", partnerCode, time.Now().UnixMilli())
			_, err := tc.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
				ID:        workflowID,
				TaskQueue: "conciliation",
			}, "ConciliationWorkflow", ConciliationInput{
				PartnerCode: partnerCode,
				Source:      source,
			})
			if err != nil {
				log.Printf("Failed to start workflow: %v", err)
				continue
			}
			log.Printf("Workflow started: %s (source: %s, partner: %s)", workflowID, source, partnerCode)

			sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      &queueURL,
				ReceiptHandle: msg.ReceiptHandle,
			})
		}
	}
}

// parseMessage handles both SNS-wrapped messages and direct SQS messages.
// SNS wraps the original message in {"Type":"Notification","Message":"{...}"}
func parseMessage(body string) (partnerCode string, source string) {
	// Try SNS envelope first
	var envelope SNSEnvelope
	if err := json.Unmarshal([]byte(body), &envelope); err == nil && envelope.Type == "Notification" {
		var inner ConciliationInput
		if err := json.Unmarshal([]byte(envelope.Message), &inner); err == nil && inner.PartnerCode != "" {
			return inner.PartnerCode, "sns"
		}
	}

	// Try direct SQS message
	var direct ConciliationInput
	if err := json.Unmarshal([]byte(body), &direct); err == nil && direct.PartnerCode != "" {
		return direct.PartnerCode, "sqs"
	}

	return "", ""
}

// ============================================================
// HTTP API — webhook triggers workflow directly
// ============================================================

func startAPI(tc client.Client) {
	mux := http.NewServeMux()

	// POST /api/v1/conciliation — triggers workflow directly
	mux.HandleFunc("/api/v1/conciliation", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var input ConciliationInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		input.Source = "api"

		workflowID := fmt.Sprintf("conciliation-%s-%d", input.PartnerCode, time.Now().UnixMilli())
		run, err := tc.ExecuteWorkflow(r.Context(), client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: "conciliation",
		}, "ConciliationWorkflow", input)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"workflow_id": run.GetID(),
			"run_id":      run.GetRunID(),
			"status":      "started",
		})
	})

	// POST /api/v1/reset — resets the conciliation DB to initial test state
	mux.HandleFunc("/api/v1/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := resetDB(r.Context()); err != nil {
			http.Error(w, "reset failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "reset complete"})
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	registerBackofficeRoutes(mux)

	log.Println("API listening on :8080")
	http.ListenAndServe(":8080", mux)
}

// resetDB resets only the processing results (collections, transaction/loan statuses)
// keeping the original test data intact but ready to be re-processed.
func resetDB(ctx context.Context) error {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL not set")
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return err
	}
	defer db.Close()

	log.Println("Resetting database...")

	// Run each reset step separately to avoid deadlocks
	_, err = db.ExecContext(ctx, `TRUNCATE collections RESTART IDENTITY`)
	if err != nil {
		return fmt.Errorf("reset collections: %w", err)
	}
	_, err = db.ExecContext(ctx, `UPDATE transactions SET status = 'pending', processed_at = NULL`)
	if err != nil {
		return fmt.Errorf("reset transactions: %w", err)
	}
	_, err = db.ExecContext(ctx, `UPDATE loans SET remaining_amount = original_amount, status = 'active', paid_at = NULL`)
	if err != nil {
		return fmt.Errorf("reset: %w", err)
	}

	// Verify
	var pending int
	var activeLoans int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transactions WHERE status = 'pending'`).Scan(&pending)
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loans WHERE status = 'active'`).Scan(&activeLoans)
	log.Printf("Database reset complete: %d pending transactions, %d active loans", pending, activeLoans)

	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
