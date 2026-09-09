package main

// Minimal ANSI SGR codes — no external dependency (T5.2's "ANSI colors, no
// lib"). colorize wraps s in code and an unconditional reset, so nested
// colorize calls can never leak one color into the next.
const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiBlue    = "\x1b[34m"
	ansiMagenta = "\x1b[35m"
	ansiCyan    = "\x1b[36m"
)

func colorize(code, s string) string { return code + s + ansiReset }
