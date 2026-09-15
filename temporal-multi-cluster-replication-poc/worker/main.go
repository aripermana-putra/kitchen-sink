// Minimal worker for the Temporal MCR PoC's dispute test (design.md Phases 16-17).
//
// One workflow, DisputeWorkflow, executes one activity, SlowActivity, with a short
// StartToCloseTimeout and a deliberately slow body — long enough to still be in-flight when
// the test stops the cluster this worker is connected to. Run two instances, one per cluster,
// via TEMPORAL_ADDRESS/TEMPORAL_NAMESPACE/CLUSTER_LABEL, to observe which cluster actually
// executes each attempt.
//
// Usage:
//
//	go run . start                 # start one DisputeWorkflow execution, then exit
//	go run . worker                # run the worker (blocks, polls the task queue)
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const TaskQueue = "dispute-tq"

func DisputeWorkflow(ctx workflow.Context) (string, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 5,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	var result string
	err := workflow.ExecuteActivity(ctx, SlowActivity).Get(ctx, &result)
	return result, err
}

func SlowActivity(ctx context.Context) (string, error) {
	info := activity.GetInfo(ctx)
	label := os.Getenv("CLUSTER_LABEL")
	log.Printf("[activity] attempt=%d cluster=%s executing (sleeping 15s)\n", info.Attempt, label)
	time.Sleep(15 * time.Second)
	result := fmt.Sprintf("completed by cluster=%s attempt=%d", label, info.Attempt)
	log.Printf("[activity] %s\n", result)
	return result, nil
}

func newClient() client.Client {
	addr := os.Getenv("TEMPORAL_ADDRESS")
	ns := os.Getenv("TEMPORAL_NAMESPACE")
	if addr == "" || ns == "" {
		log.Fatalln("TEMPORAL_ADDRESS and TEMPORAL_NAMESPACE must both be set")
	}
	c, err := client.Dial(client.Options{HostPort: addr, Namespace: ns})
	if err != nil {
		log.Fatalln("unable to create client:", err)
	}
	return c
}

func runWorker() {
	c := newClient()
	defer c.Close()

	w := worker.New(c, TaskQueue, worker.Options{})
	w.RegisterWorkflow(DisputeWorkflow)
	w.RegisterActivity(SlowActivity)

	log.Printf("worker starting: cluster=%s address=%s namespace=%s task_queue=%s\n",
		os.Getenv("CLUSTER_LABEL"), os.Getenv("TEMPORAL_ADDRESS"), os.Getenv("TEMPORAL_NAMESPACE"), TaskQueue)

	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalln("worker failed:", err)
	}
}

func startWorkflow() {
	c := newClient()
	defer c.Close()

	workflowID := os.Getenv("WORKFLOW_ID")
	if workflowID == "" {
		workflowID = "dispute-wf-1"
	}

	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: TaskQueue,
	}, DisputeWorkflow)
	if err != nil {
		log.Fatalln("unable to start workflow:", err)
	}
	log.Printf("started workflow: WorkflowID=%s RunID=%s\n", we.GetID(), we.GetRunID())
}

func main() {
	if len(os.Args) < 2 {
		log.Fatalln("usage: go run . [start|worker]")
	}
	switch os.Args[1] {
	case "start":
		startWorkflow()
	case "worker":
		runWorker()
	default:
		log.Fatalln("unknown command:", os.Args[1], "— usage: go run . [start|worker]")
	}
}
