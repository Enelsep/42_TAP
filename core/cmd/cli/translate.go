package main

import "strings"

// translateInput maps a few natural phrasings onto their RFC verb (T5.2):
// "go north" → "MOVE north", "say hi" → "CHAT ROOM hi", etc. Trigger words
// never collide with an RFC verb, so everything else — full RFC syntax
// included — passes through unchanged.
func translateInput(line string) string {
	verb, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
	switch strings.ToLower(verb) {
	case "go":
		return "MOVE " + rest
	case "say":
		return "CHAT ROOM " + rest
	case "shout":
		return "CHAT GLOBAL " + rest
	case "gsay":
		return "CHAT GROUP " + rest
	default:
		return line
	}
}
