package main

import (
	"flag"
	"log"

	"github.com/Enelsep/42_TAP/core/server"
	"github.com/Enelsep/42_TAP/core/world"
)

func main() {
	addr := flag.String("addr", ":4241", "listen address")
	path := flag.String("world", "data/world.json", "world data file")
	flag.Parse()

	w, err := world.Load(*path)
	if err != nil {
		log.Fatalf("tap server: %v", err)
	}
	if err := w.Validate(); err != nil {
		log.Fatalf("tap server: %s is invalid:\n%v", *path, err)
	}
	log.Printf("tap server: loaded %s — %d rooms, %d items, %d npcs, %d quests",
		*path, len(w.Locations), len(w.Items), len(w.NPCs), len(w.Quests))

	srv := server.New(*addr, w)
	if err := srv.Run(); err != nil {
		log.Fatalf("tap server: %v", err)
	}
}
