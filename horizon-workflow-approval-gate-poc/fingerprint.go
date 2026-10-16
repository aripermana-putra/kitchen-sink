package main

import (
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"
)

const (
	fingerprintReason = "poc fingerprint reason (not a fingerprint field)"
	raceCreations     = 5
	racePairsPerGap   = 3
)

var raceGaps = []time.Duration{0, 100 * time.Millisecond, 300 * time.Millisecond, time.Second}

// cancelWhenReady cancels a production workflow, retrying because the first
// step is entered asynchronously and cancel is refused until then.
func (c *workflowClient) cancelWhenReady(id string) {
	if id == "" {
		return
	}
	var err error
	for i := 0; i < settleAttempts; i++ {
		if err = c.triggerJobAction(id, "cancel", map[string]any{}); err == nil {
			return
		}
		time.Sleep(settleInterval)
	}
	fmt.Printf("warning: cancel %s: %v\n", id, err)
}

// runFingerprintRaceWindow issues pairs of creations of one new payload, the
// second delayed by each gap, and reports how many of each pair were accepted.
// Every accepted workflow is cancelled afterwards.
func runFingerprintRaceWindow(c *workflowClient, tenantRNS, kindID string, version int, base map[string]any) {
	base["reason"] = fingerprintReason
	var created []string
	for _, gap := range raceGaps {
		for trial := 1; trial <= racePairsPerGap; trial++ {
			params := maps.Clone(base)
			params["resource_id"] = fmt.Sprintf("%s-gap%dms-t%d", base["resource_id"], gap.Milliseconds(), trial)
			ids := make([]string, 2)
			var wg sync.WaitGroup
			for i := range ids {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if i == 1 {
						time.Sleep(gap)
					}
					ids[i], _ = c.createProductionWorkflow(tenantRNS, kindID, version, params)
				}()
			}
			wg.Wait()
			accepted := 0
			for _, id := range ids {
				if id != "" {
					accepted++
					created = append(created, id)
				}
			}
			fmt.Printf("RESULT gap=%dms trial=%d accepted=%d of 2 %v\n", gap.Milliseconds(), trial, accepted, ids)
		}
	}
	for _, id := range created {
		c.cancelWhenReady(id)
	}
	fmt.Printf("\ncreated workflows: %v\n", created)
}

// runFingerprintCases creates production workflows to observe fingerprint
// behavior: the first one stays open so its emails can be read, a duplicate
// and a payload missing a field are expected to be refused, and a repeat is
// tried after the first is cancelled. Two further cases probe the check
// across a newly published version and under simultaneous creations.
// publishNext publishes the same definition again and returns the new version.
// Creation errors are printed, not fatal; every created workflow is cancelled.
func runFingerprintCases(c *workflowClient, tenantRNS, kindID string, version int, base map[string]any, publishNext func() (int, error)) {
	base["reason"] = fingerprintReason
	var (
		mu      sync.Mutex
		created []string
	)
	create := func(label string, version int, params map[string]any) string {
		fmt.Printf("\n--- %s ---\n", label)
		id, err := c.createProductionWorkflow(tenantRNS, kindID, version, params)
		if err != nil {
			fmt.Printf("RESULT %s: refused: %v\n", label, err)
			return ""
		}
		fmt.Printf("RESULT %s: created %s\n", label, id)
		mu.Lock()
		created = append(created, id)
		mu.Unlock()
		return id
	}
	cancel := c.cancelWhenReady
	variant := func(edit func(p map[string]any)) map[string]any {
		p := maps.Clone(base)
		edit(p)
		return p
	}
	withSuffix := func(suffix string) map[string]any {
		return variant(func(p map[string]any) { p["resource_id"] = fmt.Sprint(base["resource_id"], suffix) })
	}

	first := create("B first workflow", version, base)
	create("C same payload while first is active", version, base)
	other := create("E different resource_id", version, withSuffix("-other"))
	create("F missing fingerprint field tenant_name", version, variant(func(p map[string]any) { delete(p, "tenant_name") }))

	cancel(first)
	cancel(other)
	cancel(create("D same payload after first is cancelled", version, base))

	crossVersion := withSuffix("-xver")
	open := create(fmt.Sprintf("G1 workflow on version %d", version), version, crossVersion)
	next, err := publishNext()
	if err != nil {
		fmt.Printf("RESULT G: could not publish a newer version: %v\n", err)
	} else {
		cancel(create(fmt.Sprintf("G2 same payload on newer version %d while version %d workflow is active", next, version), next, crossVersion))
		version = next
	}
	cancel(open)

	race := withSuffix("-race")
	fmt.Printf("\n--- H %d simultaneous creations of one payload ---\n", raceCreations)
	var wg sync.WaitGroup
	for i := 0; i < raceCreations; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			create(fmt.Sprintf("H%d", i+1), version, race)
		}(i)
	}
	wg.Wait()

	for _, id := range created {
		cancel(id)
	}
	fmt.Printf("\ncreated workflows: %v\n", created)
}

// runFingerprintTypes creates one workflow whose fingerprint includes values
// of several JSON types, leaves it open so its review email can be read, then
// repeats the payload to show how the 409 reports the fingerprint, and cancels.
func runFingerprintTypes(c *workflowClient, tenantRNS, kindID string, version int, base map[string]any) {
	base["reason"] = fingerprintReason
	base["quota"] = 12.5
	base["dry_run"] = true
	base["labels"] = map[string]any{"env": "qa", "team": "ucp"}
	base["tags"] = []any{"alpha", "beta", 3}
	base["note"] = strings.Repeat("long-note-segment-", 35)
	base["markup"] = `<b>bold</b> & "quoted" 'single' <script>x</script>`

	fmt.Println("\n--- T1 workflow with typed fingerprint values ---")
	id, err := c.createProductionWorkflow(tenantRNS, kindID, version, base)
	if err != nil {
		fmt.Printf("RESULT T1: refused: %v\n", err)
		return
	}
	fmt.Printf("RESULT T1: created %s resource_id=%v\n", id, base["resource_id"])

	fmt.Println("\n--- T2 same payload ---")
	time.Sleep(2 * time.Second)
	if _, err := c.createProductionWorkflow(tenantRNS, kindID, version, base); err != nil {
		fmt.Printf("RESULT T2: refused: %v\n", err)
	} else {
		fmt.Println("RESULT T2: created (unexpected)")
	}

	fmt.Println("\nWaiting 60s so the review email can be read before the workflow is cancelled.")
	time.Sleep(60 * time.Second)
	c.cancelWhenReady(id)
}
