package main

import (
	"crypto/rand"
	"fmt"
	"maps"
	"sync"
	"time"
)

var duplicateCheckGaps = []time.Duration{0, 300 * time.Millisecond, 600 * time.Millisecond, time.Second, 1500 * time.Millisecond}

const (
	duplicateCheckTrials = 2
	requestIDSettle      = 2 * time.Second
)

// runIdempotencyCases probes the two candidate idempotency inputs: the
// X-Request-ID header and POST /jobs:check-duplicate. Creation errors are
// printed, not fatal; every created workflow is cancelled at the end.
func runIdempotencyCases(c *workflowClient, tenantRNS, kindID string, version int, base map[string]any) {
	base["reason"] = fingerprintReason
	var (
		mu      sync.Mutex
		created []string
	)
	record := func(id string) {
		if id != "" {
			mu.Lock()
			created = append(created, id)
			mu.Unlock()
		}
	}
	withID := func(suffix string) map[string]any {
		p := maps.Clone(base)
		p["resource_id"] = fmt.Sprint(base["resource_id"], suffix)
		return p
	}
	create := func(label string, params map[string]any) string {
		id, err := c.createProductionWorkflow(tenantRNS, kindID, version, params)
		if err != nil {
			fmt.Printf("RESULT %s: refused: %v\n", label, err)
			return ""
		}
		fmt.Printf("RESULT %s: created %s\n", label, id)
		record(id)
		return id
	}

	requestID := newUUID()
	fmt.Printf("\n--- X-Request-ID %s ---\n", requestID)
	c.requestID = requestID
	create("I1 header set, payload P1", withID("-p1"))
	time.Sleep(requestIDSettle)
	create("I2 same header, different payload P2", withID("-p2"))
	time.Sleep(requestIDSettle)
	create("I3 same header, payload P1 again", withID("-p1"))
	c.requestID = ""
	time.Sleep(requestIDSettle)
	create("I4 no header, payload P3 (control)", withID("-p3"))
	create("I5 no header, payload P3 again immediately (control)", withID("-p3"))

	fmt.Println("\n--- check-duplicate against active and unknown payloads ---")
	time.Sleep(requestIDSettle)
	for label, params := range map[string]map[string]any{"active P3": withID("-p3"), "unknown P4": withID("-p4")} {
		dup, err := c.checkDuplicate(kindID, version, params)
		fmt.Printf("RESULT check-duplicate %s: is_duplicate=%v err=%v\n", label, dup, err)
	}

	fmt.Println("\n--- check-duplicate issued after a creation ---")
	for _, gap := range duplicateCheckGaps {
		for trial := 1; trial <= duplicateCheckTrials; trial++ {
			params := withID(fmt.Sprintf("-chk%dms-t%d", gap.Milliseconds(), trial))
			var wg sync.WaitGroup
			var id string
			wg.Add(1)
			go func() {
				defer wg.Done()
				id, _ = c.createProductionWorkflow(tenantRNS, kindID, version, params)
			}()
			time.Sleep(gap)
			dup, err := c.checkDuplicate(kindID, version, params)
			wg.Wait()
			record(id)
			fmt.Printf("RESULT check gap=%dms trial=%d created=%v is_duplicate=%v err=%v\n", gap.Milliseconds(), trial, id != "", dup, err)
		}
	}

	for _, id := range created {
		c.cancelWhenReady(id)
	}
	fmt.Printf("\ncreated workflows: %v\n", created)
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
