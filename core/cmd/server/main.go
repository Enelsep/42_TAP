package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/Enelsep/42_TAP/core/server"
	"github.com/Enelsep/42_TAP/core/world"
)

func main() {
	// JSON, leveled, timestamped — the whole of the subject's logging
	// checklist, for zero dependencies (D17). Every package under
	// core/server logs through this default logger.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	addr := flag.String("addr", ":4241", "listen address")
	path := flag.String("world", "data/world.json", "world data file")
	flag.Parse()

	w, err := world.Load(*path)
	if err != nil {
		slog.Error("world load failed", "path", *path, "err", err)
		os.Exit(1)
	}
	if err := w.Validate(); err != nil {
		slog.Error("world invalid", "path", *path, "err", err)
		os.Exit(1)
	}
	slog.Info("world loaded", "path", *path,
		"rooms", len(w.Locations), "items", len(w.Items), "npcs", len(w.NPCs), "quests", len(w.Quests))

	srv := server.New(*addr, w)
	if err := srv.Run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
