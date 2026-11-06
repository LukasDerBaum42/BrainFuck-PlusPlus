package main
 
import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	// "runtime"
)


func termInit() {
	cmd := exec.Command("stty", "-icanon", "min", "1", "-echo")
	cmd.Stdin = os.Stdin
	cmd.Run()
}

func termRestore() {
	cmd := exec.Command("stty", "sane")
	cmd.Stdin = os.Stdin
	cmd.Run()
}

func pollKey() byte {
	buf := make([]byte, 1)
	n, _ := os.Stdin.Read(buf)
	if n > 0 {
		return buf[0]
	}
	return 0
}

// ── stack types ───────────────────────────────────────────────────────────────

const STACK_MAX_ENTRIES = 256

// stackEntry is one element on the bf++ value stack.
// size == 0 is the underflow sentinel (1 zero byte).
type stackEntry struct {
	size uint8
	data [255]byte
}

// bfStack is a per-call-frame value stack.
type bfStack struct {
	entries [STACK_MAX_ENTRIES]stackEntry
	top     int // index of next free slot; 0 = empty
}

func (s *bfStack) push(e stackEntry) bool {
	if s.top >= STACK_MAX_ENTRIES {
		return false // overflow
	}
	s.entries[s.top] = e
	s.top++
	return true
}

// pop returns the top entry. On underflow returns a 1-zero-byte entry and true for underflow.
func (s *bfStack) pop() (stackEntry, bool) {
	if s.top == 0 {
		var zero stackEntry
		zero.size = 1
		return zero, true // underflow
	}
	s.top--
	return s.entries[s.top], false
}

func (s *bfStack) peek() (stackEntry, bool) {
	if s.top == 0 {
		var zero stackEntry
		zero.size = 1
		return zero, true
	}
	return s.entries[s.top-1], false
}

// ── sig slot ─────────────────────────────────────────────────────────────────

// sigSlot describes one arg or return slot in a function signature.
// If wildcard is true, any size is accepted (the 'n' slot).
// Otherwise exactSize gives the required byte width.
type sigSlot struct {
	wildcard  bool
	exactSize uint8
	type_     string
	// ints will use i32 and floats will use f32 with the number of bits specified
	// eg. uint8 is u8, int64 is i64, float32 is f32 and so on
	// strings are always str
	// structs are given by name
}

type instruction struct {
	instr    uint8
	value    uint64
	f_name   string
	other    any
	switches map[byte]string
}

type function struct {
	name      string
	instructs []instruction
	// isMacro == true  → no own stack; body runs on caller's stack directly
	// isMacro == false → owns a stack; args/returns transferred per signature
	isMacro   bool
	calls     []string
	args      []sigSlot // nil when isMacro
	returns   []sigSlot // nil when isMacro; empty slice = no returns declared
}

type extern struct {
	name      string
	args      []sigSlot
	returns   []sigSlot
}

type frame struct {
	bytecode []instruction
	counter  uint64
}
type tape_t struct {
	name     string
	data     [0xFFFF + 1]byte
	position uint16
}

type loopInfo struct {
    startIdx uint64 // bytecode index of the matching [
    count    uint64 // iterations completed
}

var FUNCTS 		map[string]function
var EXTERN 		map[string]extern
var TAPES 		map[string]tape_t
var IMPORTED 	map[string]bool
var HEADER 		[]string
var LIB_PATH 	[]string

// callSite records a name reference (function call, tape switch, switch case)
// together with enough context to point at it in an error message.
type callSite struct {
	name       string
	nameOffset uint64  // byte offset of the first character of the name within code
	ctx        compCtx // compilation context at the call site
	code       string  // the local source slice that was being compiled
}

var called_functs []callSite
var called_tapes  []callSite

// keywordBytes lists every single-byte character that is a language keyword.
// The multi-byte keyword '§' is checked separately in validateName.
const keywordBytes = "+-<>[].,!?$§*:^@(){}"

// validateName returns an error if name contains a keyword character or whitespace.
// nameStart is the byte offset of name[0] within code (used for error reporting).
func validateName(name string, nameStart uint64, ctx compCtx, code string) error {
	for i, ch := range name {
		off := nameStart + uint64(i)
		chStr := string(ch)
		if strings.ContainsRune(keywordBytes, ch) || chStr == "§" {
			return showError(ctx, code, off, "error",
				fmt.Sprintf("keyword character '%s' is not allowed in a name", chStr))
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			return showError(ctx, code, off, "error",
				"whitespace is not allowed in a name")
		}
	}
	return nil
}


func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
