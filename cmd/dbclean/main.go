package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func main() {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".metronous", "data", "tracking.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Println("ERROR:", err)
		return
	}
	defer db.Close()

	ctx := context.Background()

	// Count before
	var total int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events").Scan(&total)
	var nullTokens int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events WHERE prompt_tokens IS NULL AND completion_tokens IS NULL").Scan(&nullTokens)

	fmt.Printf("Total events: %d\n", total)
	fmt.Printf("Events without token data: %d\n", nullTokens)

	if len(os.Args) > 1 && os.Args[1] == "--purge" {
		res, err := db.ExecContext(ctx, "DELETE FROM events WHERE prompt_tokens IS NULL AND completion_tokens IS NULL")
		if err != nil {
			fmt.Println("ERROR deleting:", err)
			return
		}
		deleted, _ := res.RowsAffected()
		fmt.Printf("\nPurged %d events without token data.\n", deleted)

		// Vacuum to reclaim space
		db.ExecContext(ctx, "VACUUM")
		fmt.Println("Database vacuumed.")
	} else {
		fmt.Println("\nRun with --purge to delete events without token data.")
	}
}
