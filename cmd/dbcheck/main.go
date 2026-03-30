package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kiosvantra/metronous/internal/store"
	sqlitestore "github.com/kiosvantra/metronous/internal/store/sqlite"
)

func main() {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".metronous", "data", "tracking.db")
	es, err := sqlitestore.NewEventStore(dbPath)
	if err != nil {
		fmt.Println("ERROR:", err)
		return
	}
	defer es.Close()

	ctx := context.Background()
	events, err := es.QueryEvents(ctx, store.EventQuery{Limit: 15})
	if err != nil {
		fmt.Println("ERROR:", err)
		return
	}

	fmt.Printf("%-12s %-22s %-30s %-10s %-10s %-10s\n", "TYPE", "AGENT", "MODEL", "IN", "OUT", "COST")
	fmt.Println("-----------------------------------------------------------------------------------------------")
	for _, e := range events {
		in := "-"
		out := "-"
		cost := "-"
		if e.PromptTokens != nil {
			in = fmt.Sprintf("%d", *e.PromptTokens)
		}
		if e.CompletionTokens != nil {
			out = fmt.Sprintf("%d", *e.CompletionTokens)
		}
		if e.CostUSD != nil {
			cost = fmt.Sprintf("$%.6f", *e.CostUSD)
		}
		fmt.Printf("%-12s %-22s %-30s %-10s %-10s %-10s\n", e.EventType, e.AgentID, e.Model, in, out, cost)
	}
	fmt.Printf("\nTotal events: %d\n", len(events))
}
