package main
 
func make_bytecode(code string, ctx compCtx) ([]instruction, []string, error) {
	var bytecode []instruction
	var functions_called []string
	var counter uint64

	type loopEntry struct {
		bcIdx     uint64
		srcOffset uint64
	}
	var loops []loopEntry
	code_len := uint64(len(code))

	for counter < code_len {
		key := code[counter]
		switch key {
		default:
			counter++
		case '/':
			counter++
			if counter < code_len && code[counter] == '/' {
				for counter < code_len && code[counter] != '\n' {
					counter++
				}
				continue
			}
		case '+', '-', '<', '>':
			var temp instruction
			counter, temp = comp_add_sub_move(counter, code, key)
			bytecode = append(bytecode, temp)
		case '[':
			loops = append(loops, loopEntry{uint64(len(bytecode)), counter})
			bytecode = append(bytecode, instruction{instr: 4, other: counter})
			counter++
		case ']':
			if len(loops) == 0 {
				return bytecode, functions_called, showError(ctx, code, counter, "error",
					"unexpected ']' — no matching '['")
			}
			top := loops[len(loops)-1]
			loops = loops[:len(loops)-1]
			bytecode[top.bcIdx].value = uint64(len(bytecode))
			bytecode = append(bytecode, instruction{instr: 5, value: top.bcIdx})
			counter++
		case '.':
			var temp instruction
			counter, temp = comp_print(counter, code)
			bytecode = append(bytecode, temp)
		case ',':
			bytecode = append(bytecode, instruction{instr: 7})
			counter++
		case '!':
			var temp instruction
			var err error
			counter, temp, err = comp_func(counter, code, ctx)
			if err != nil {
				return bytecode, functions_called, err
			}
			bytecode = append(bytecode, temp)
		case '?':
			var temp instruction
			var err error
			counter, temp, err = comp_load_func(counter, code, ctx)
			if err != nil {
				return bytecode, functions_called, err
			}
			bytecode = append(bytecode, temp)
			functions_called = append(functions_called, temp.f_name)
		case '$':
			var temp instruction
			var err error
			counter, temp, err = comp_switch(counter, code, ctx)
			if err != nil {
				return bytecode, functions_called, err
			}
			for _, i := range temp.switches {
				functions_called = append(functions_called, i)
			}
			bytecode = append(bytecode, temp)
		case '§':
			var temp instruction
			var err error
			counter, temp, err = comp_create_tape(counter, code, ctx)
			if err != nil {
				return bytecode, functions_called, err
			}
			bytecode = append(bytecode, temp)
		case ':':
			var temp instruction
			var err error
			counter, temp, err = comp_switch_tape(counter, code, ctx)
			if err != nil {
				return bytecode, functions_called, err
			}
			bytecode = append(bytecode, temp)

		case '^':
			var temp instruction
			var err error
			counter, temp, err = comp_push(counter, code, ctx)
			if err != nil {
				return bytecode, functions_called, err
			}
			bytecode = append(bytecode, temp)
		case 'v':
			var temp instruction
			counter, temp = comp_pop(counter)
			bytecode = append(bytecode, temp)
		case '@':
			var temp []instruction
			var err error
			counter, temp, err = comp_extern(counter, code, ctx)
			if err != nil {
				return bytecode, functions_called, err
			}
			bytecode = append(bytecode, temp...)
		}
	}

	if len(loops) > 0 {
		top := loops[len(loops)-1]
		return bytecode, functions_called, showErrorWithNote(ctx, code,
			code_len, "reached end of input with unclosed '['",
			top.srcOffset, "'[' opened here",
		)
	}

	return bytecode, functions_called, nil
}