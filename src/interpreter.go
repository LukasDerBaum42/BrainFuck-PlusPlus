package main

import (
	"fmt"
	"os"
)

// ── interpreter ───────────────────────────────────────────────────────────────

// transferArgs pops args from caller's stack onto callee's stack,
// verifying sizes against the declared signature.
// First arg slot matches the top of caller stack.
func transferArgs(callerStack *bfStack, calleeStack *bfStack, slots []sigSlot, funcName string) bool {
	for i, slot := range slots {
		entry, underflow := callerStack.pop()
		if underflow {
			fmt.Fprintf(os.Stderr,
				"runtime error: function '%s' arg %d: stack underflow during argument transfer\n",
				funcName, i+1)
			return false
		}
		if !slot.wildcard && entry.size != slot.exactSize {
			fmt.Fprintf(os.Stderr,
				"runtime error: function '%s' arg %d: expected %d-byte element, got %d-byte element\n",
				funcName, i+1, slot.exactSize, entry.size)
			return false
		}
		if !calleeStack.push(entry) {
			fmt.Fprintf(os.Stderr,
				"runtime error: function '%s': callee stack overflow during argument transfer\n",
				funcName)
			return false
		}
	}
	return true
}

// transferReturns pops return values from callee's stack onto caller's stack.
// The first listed return slot is the bottom of the values to transfer
// (so the last listed return ends up on top of caller stack).
func transferReturns(calleeStack *bfStack, callerStack *bfStack, slots []sigSlot, funcName string) bool {
	if len(slots) == 0 {
		return true
	}
	// collect from callee (top-first) then reverse-push onto caller
	// so that caller ends up with first-return at bottom, last-return on top
	collected := make([]stackEntry, len(slots))
	for i, slot := range slots {
		entry, underflow := calleeStack.pop()
		if underflow {
			fmt.Fprintf(os.Stderr,
				"runtime error: function '%s' return %d: stack underflow during return transfer\n",
				funcName, i+1)
			return false
		}
		if !slot.wildcard && entry.size != slot.exactSize {
			fmt.Fprintf(os.Stderr,
				"runtime error: function '%s' return %d: expected %d-byte element, got %d-byte element\n",
				funcName, i+1, slot.exactSize, entry.size)
			return false
		}
		collected[i] = entry
	}
	// push in reverse so first return ends up deepest, last return on top
	for i := len(collected) - 1; i >= 0; i-- {
		if !callerStack.push(collected[i]) {
			fmt.Fprintf(os.Stderr,
				"runtime error: function '%s': caller stack overflow during return transfer\n",
				funcName)
			return false
		}
	}
	return true
}

// interpFrame extends the execution frame with a value stack pointer.
type interpFrame struct {
	bytecode []instruction
	counter  uint64
	stack    *bfStack  // effective stack; macros snapshot the caller's stack
	retSlots []sigSlot // nil or empty when the frame has no returns
	funcName string    // used in runtime error messages
}

// pushCallFrame pushes the callee frame for fn. Macros share the caller's
// value stack; functions receive their own stack after argument transfer.
func pushCallFrame(stack *[]interpFrame, fn function) {
	caller := (*stack)[len(*stack)-1].stack

	if fn.isMacro {
		*stack = append(*stack, interpFrame{
			bytecode: fn.instructs,
			stack:    caller,
		})
		return
	}

	callee := &bfStack{}
	if !transferArgs(caller, callee, fn.args, fn.name) {
		os.Exit(1)
	}
	*stack = append(*stack, interpFrame{
		bytecode: fn.instructs,
		stack:    callee,
		retSlots: fn.returns,
		funcName: fn.name,
	})
}

func interpret_bytecode(entry []instruction) {
	tape_map := make(map[string]uint16)
	tapes_array := make([]tape_t, 0, len(TAPES))

	var temp uint16 = 0
	for _, elem := range TAPES {
		tapes_array = append(tapes_array, tape_t{name: elem.name})
		tape_map[elem.name] = temp
		temp++
	}
	loop_stack := make([]loopInfo, 0)

	tape := &tapes_array[tape_map["main"]]

	// root stack for main
	rootStack := &bfStack{}

	stack := []interpFrame{{bytecode: entry, counter: 0, stack: rootStack}}

	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.counter >= uint64(len(top.bytecode)) {
			// Frame finished: transfer returns to the caller before popping.
			if len(top.retSlots) > 0 && len(stack) > 1 {
				caller := stack[len(stack)-2].stack
				if !transferReturns(top.stack, caller, top.retSlots, top.funcName) {
					os.Exit(1)
				}
			}
			stack = stack[:len(stack)-1]
			continue
		}
		instr := top.bytecode[top.counter]

		depth := len(loop_stack)
		firstIter := true
		for _, li := range loop_stack {
			if li.count > 0 {
				firstIter = false
				break
			}
		}

		if *interpreter_debug {
			if firstIter || *show_loop {
				if depth > 0 && *show_loop {
					fmt.Print("loop depth:", depth, " ")
					fmt.Print("loop count:", loop_stack[depth-1].count, " ")
				}
				fmt.Print("counter:", top.counter, " ")
				fmt.Print("stack len:", len(stack), " ")
				fmt.Print("on tape:", tape.name, " ")
				fmt.Print("tape position:", tape.position, " ")
				print_inst(instr, false)
			}
		}

		switch instr.instr {
		default:
			top.counter++
		case 0:
			tape.data[tape.position] += byte(instr.value)
			top.counter++
		case 1:
			tape.data[tape.position] -= byte(instr.value)
			top.counter++
		case 2:
			tape.position -= uint16(instr.value)
			top.counter++
		case 3:
			tape.position += uint16(instr.value)
			top.counter++
		case 4:
			if tape.data[tape.position] == 0 {
				if *interpreter_debug && (firstIter || *show_loop) {
					fmt.Print("loop skipped\n")
				}
				top.counter = instr.value
			} else {
				if len(loop_stack) == 0 || loop_stack[len(loop_stack)-1].startIdx != top.counter {
					loop_stack = append(loop_stack, loopInfo{startIdx: top.counter})
				}
			}
			top.counter++
		case 5:
			if tape.data[tape.position] != 0 {
				if len(loop_stack) > 0 {
					loop_stack[len(loop_stack)-1].count++
				}
				top.counter = instr.value
			} else {
				if len(loop_stack) > 0 {
					info := loop_stack[len(loop_stack)-1]
					loop_stack = loop_stack[:len(loop_stack)-1]
					if *interpreter_debug && (firstIter || *show_loop) {
						fmt.Print("loop done — depth:", len(loop_stack)+1,
							" iterations:", info.count+1, "\n")
					}

				}
				top.counter++
			}
		case 6:
			switch instr.value {
			case 0:
				fmt.Print(string(tape.data[tape.position]))
			case 1:
				fmt.Print(tape.data[tape.position])
			case 2:
				fmt.Printf("%02x", tape.data[tape.position])
			case 3:
				fmt.Printf("%08b", tape.data[tape.position])
			}
			top.counter++
		case 7:
			tape.data[tape.position] = pollKey()
			top.counter++
		case 9:
			fn := FUNCTS[instr.f_name]
			top.counter++
			pushCallFrame(&stack, fn)
		case 10:
			key := tape.data[tape.position]
			top.counter++
			var targetName string
			if val, ok := instr.switches[key]; ok {
				targetName = val
			} else if instr.value == 1 {
				targetName = instr.f_name
			}
			if targetName != "" {
				pushCallFrame(&stack, FUNCTS[targetName])
			}
		case 13:
			if instr.value == 0 {
				tape = &tapes_array[tape_map["main"]]
			} else {
				tape = &tapes_array[tape_map[instr.f_name]]
			}
			top.counter++

		// ── push (copy) ^^
		case 14:
			n := uint8(instr.value)
			var entry stackEntry
			entry.size = n
			for i := uint8(0); i < n; i++ {
				entry.data[i] = tape.data[tape.position+uint16(i)]
			}
			vs := top.stack
			if !vs.push(entry) {
				fmt.Fprintln(os.Stderr, "runtime error: value stack overflow on push")
				os.Exit(1)
			}
			top.counter++

		// ── push (move) ^^^
		case 15:
			n := uint8(instr.value)
			var entry stackEntry
			entry.size = n
			for i := uint8(0); i < n; i++ {
				entry.data[i] = tape.data[tape.position+uint16(i)]
				tape.data[tape.position+uint16(i)] = 0
			}
			vs := top.stack
			if !vs.push(entry) {
				fmt.Fprintln(os.Stderr, "runtime error: value stack overflow on move-push")
				os.Exit(1)
			}
			top.counter++

			// ── pop ──────────────────────────────────────────────────────────
		case 16:
			vs := top.stack
			entry, underflow := vs.pop()
			if underflow {
				// underflow: write single zero byte, don't abort
				tape.data[tape.position] = 0
			} else {
				for i := uint8(0); i < entry.size; i++ {
					tape.data[tape.position+uint16(i)] = entry.data[i]
				}
			}
			top.counter++
		}
	}
}
