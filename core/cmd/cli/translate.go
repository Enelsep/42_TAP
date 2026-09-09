package main

import "strings"

// translateInput maps a handful of natural phrasings onto their RFC verb
// (T5.2, the subject's "translating interface" choice over T5.1's raw
// pass-through): "go north" → "MOVE north", "say hi" → "CHAT ROOM hi", and
// the same for the other two CHAT scopes. None of the trigger words collide
// with an actual RFC verb, so this can never shadow one — everything else,
// including every RFC verb typed directly (case-insensitive per §4.2),
// goes out completely unchanged. That is what keeps full RFC syntax
// working: this function only ever adds recognized shortcuts, it never
// rejects or rewrites anything it doesn't specifically know.
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
