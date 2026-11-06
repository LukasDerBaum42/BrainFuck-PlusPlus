package main

import (
	"fmt"
	"os"
	"strconv"
)

// ── compiler ──────────────────────────────────────────────────────────────────

func comp_add_sub_move(counter uint64, code string, symbol byte) (uint64, instruction) {
	instuc := instruction{}
	switch symbol {
	case '+':
		instuc.instr = 0
	case '-':
		instuc.instr = 1
	case '<':
		instuc.instr = 2
	case '>':
		instuc.instr = 3
	}
	counter++
	if counter >= uint64(len(code)) {
		instuc.value = 1
		return counter, instuc
	}
	if code[counter] == symbol {
		var temp uint64 = 2
		counter++
		for counter < uint64(len(code)) && code[counter] == symbol {
			counter++
			temp++
		}
		instuc.value = temp
	} else if code[counter] >= '0' && code[counter] <= '9' {
		temp := string(code[counter])
		counter++
		for counter < uint64(len(code)) && code[counter] >= '0' && code[counter] <= '9' {
			temp += string(code[counter])
			counter++
		}
		out, _ := strconv.ParseUint(temp, 10, 64)
		instuc.value = out
	} else {
		instuc.value = 1
	}
	return counter, instuc
}

func comp_print(counter uint64, code string) (uint64, instruction) {
	instuc := instruction{instr: 6, value: 0}
	counter++
	if counter >= uint64(len(code)) {
		return counter, instuc
	}
	switch code[counter] {
	case '0':
		counter++
	case '1':
		instuc.value = 1
		counter++
	case '2':
		instuc.value = 2
		counter++
	case '3':
		instuc.value = 3
		counter++
	case '4':
		instuc.value = 4
		counter++
	}
	return counter, instuc
}

// parseSigSlots parses a comma-separated list of sig slots inside ( ).
// On entry counter points at the '(' character.
// Returns counter past the closing ')'.
func parseSigSlots(counter uint64, code string, ctx compCtx) (uint64, []sigSlot, error) {
	openParen := counter
	counter++ // skip '('

	var slots []sigSlot

	for {
		// skip whitespace
		for counter < uint64(len(code)) && (code[counter] == ' ' || code[counter] == '\t' || code[counter] == '\n' || code[counter] == '\r') {
			counter++
		}
		if counter >= uint64(len(code)) {
			return counter, slots, showErrorWithNote(ctx, code,
				counter, "signature never closed with ')'",
				openParen, "opened here",
			)
		}
		if code[counter] == ')' {
			counter++ // skip ')'
			break
		}
		// skip comma between slots
		if code[counter] == ',' {
			counter++
			continue
		}
		if code[counter] == 'n' {
			var err error
			var slot sigSlot
			counter, slot, err = parseSigSlot(counter, code, ctx)
			if err != nil {
				return counter, slots, err
			}
			slots = append(slots, slot)
			//counter++ // skip 'n'
			continue
		}
		if code[counter] == '^' {
			var err error
			var slot sigSlot
			counter, slot, err = parseSigSlot(counter, code, ctx)
			if err != nil {
				return counter, slots, err
			}
			slots = append(slots, slot)
			// counter++ // skip '^'
			continue
		}

		return counter, nil, showError(ctx, code, counter, "error",
			fmt.Sprintf("unexpected character '%c' in function signature", code[counter]))
	}
	return counter, slots, nil
}

func parseSigSlot(counter uint64, code string, ctx compCtx) (uint64, sigSlot, error) {
	openParen := counter
	slot := sigSlot{}
	// 'n' wildcard
	for counter < uint64(len(code)) && (code[counter] == ' ' || code[counter] == '\t' || code[counter] == '\n' || code[counter] == '\r') {
		counter++
	}
	if counter >= uint64(len(code)) {
		return counter, slot, showErrorWithNote(ctx, code,
			counter, "signature never closed with ')'",
			openParen, "opened here",
		)
	}
	if code[counter] == 'n' {
		slot.wildcard = true
		counter++
	}
	// '^' followed by number
	if code[counter] == '^' {
		counter++ // skip '^'
		if counter >= uint64(len(code)) || code[counter] < '1' || code[counter] > '9' {
			return counter, slot, showError(ctx, code, counter-1, "error",
				"expected a size (1–255) after '^' in function signature")
		}
		temp := string(code[counter])
		counter++
		for counter < uint64(len(code)) && code[counter] >= '0' && code[counter] <= '9' {
			temp += string(code[counter])
			counter++
		}
		sz, err := strconv.ParseUint(temp, 10, 64)
		if err != nil || sz == 0 || sz > 255 {
			return counter, slot, showError(ctx, code, counter, "error",
				fmt.Sprintf("signature size must be 1–255, got %s", temp))
		}
		slot.exactSize = uint8(sz)
	}
	for counter < uint64(len(code)) && (code[counter] == ' ' || code[counter] == '\t' || code[counter] == '\n' || code[counter] == '\r') {
		counter++
	}
	if code[counter] == ':' {
		counter++ // skip '('
		for counter < uint64(len(code)) && (code[counter] == ' ' || code[counter] == '\t' || code[counter] == '\n' || code[counter] == '\r') {
			counter++
		}
		type_name := ""
		for counter < uint64(len(code)) && code[counter] != ')' && code[counter] != '{' && code[counter] != '(' && code[counter] != ',' && code[counter] != ' ' {
			type_name += string(code[counter])
			counter++
		}
		slot.type_ = type_name
	}
	return counter, slot, nil
}

func comp_func(counter uint64, code string, ctx compCtx) (uint64, instruction, error) {
	instr := instruction{instr: 8}
	counter++

	if counter >= uint64(len(code)) || code[counter] != '(' {
		return counter, instr, showError(ctx, code, counter-1, "error",
			"expected '(' after '!' to open function name")
	}
	counter++ // skip '('

	n_start := counter
	for counter < uint64(len(code)) && code[counter] != ')' && code[counter] != '{' {
		if counter+1 < uint64(len(code)) && code[counter] == '/' && code[counter+1] == '/' {
			for counter < uint64(len(code)) && code[counter] != '\n' {
				counter++
			}
			continue
		}
		counter++
	}
	if counter >= uint64(len(code)) {
		return counter, instr, showErrorWithNote(ctx, code,
			counter, "function name never closed with ')'",
			n_start, "function name started here",
		)
	}
	if code[counter] == '{' {
		return counter, instr, showErrorWithNote(ctx, code,
			counter, "unexpected '{' inside function name — missing ')'",
			n_start, "function name started here",
		)
	}
	name := code[n_start:counter]
	if err := validateName(name, n_start, ctx, code); err != nil {
		return counter, instr, err
	}
	counter++ // skip ')'

	// ── parse optional arg/return signatures ──────────────────────────────
	isMacro := true
	var argSlots []sigSlot
	var retSlots []sigSlot

	// skip whitespace
	for counter < uint64(len(code)) && (code[counter] == ' ' || code[counter] == '\t') {
		counter++
	}

	if counter < uint64(len(code)) && code[counter] == '(' {
		// has arg signature → function mode
		isMacro = false
		var err error
		counter, argSlots, err = parseSigSlots(counter, code, ctx)
		if err != nil {
			return counter, instr, err
		}

		// skip whitespace
		for counter < uint64(len(code)) && (code[counter] == ' ' || code[counter] == '\t') {
			counter++
		}

		// optional return signature
		if counter < uint64(len(code)) && code[counter] == '(' {
			counter, retSlots, err = parseSigSlots(counter, code, ctx)
			if err != nil {
				return counter, instr, err
			}
		}
		// if no return parens, retSlots stays nil → no returns
	}

	// skip whitespace before '{'
	for counter < uint64(len(code)) && (code[counter] == ' ' || code[counter] == '\t') {
		counter++
	}

	if counter >= uint64(len(code)) || code[counter] != '{' {
		return counter, instr, showError(ctx, code, counter, "error",
			fmt.Sprintf("expected '{' to open body of function '%s'", name))
	}
	bodyOpen := counter
	counter++ // skip '{'

	c_start := counter
	nested := 0
	for counter < uint64(len(code)) && (code[counter] != '}' || nested != 0) {
		if counter+1 < uint64(len(code)) && code[counter] == '/' && code[counter+1] == '/' {
			for counter < uint64(len(code)) && code[counter] != '\n' {
				counter++
			}
			continue
		}
		if code[counter] == '{' {
			nested++
		} else if code[counter] == '}' {
			nested--
		}
		counter++
	}
	if counter >= uint64(len(code)) {
		return counter, instr, showErrorWithNote(ctx, code,
			counter, fmt.Sprintf("body of function '%s' is never closed with '}'", name),
			bodyOpen, "body opened here",
		)
	}

	new_code := code[c_start:counter]
	counter++ // skip '}'

	childCtx := compCtx{
		globalSrc:  ctx.globalSrc,
		baseOffset: ctx.baseOffset + c_start,
		funcName:   name,
	}

	new_bytecode, functions_called, err := make_bytecode(new_code, childCtx)
	if err != nil {
		showContext(ctx, name)
		return counter, instr, err
	}

	instr.f_name = name
	FUNCTS[name] = function{
		name:      name,
		instructs: new_bytecode,
		isMacro:   isMacro,
		args:      argSlots,
		returns:   retSlots,
		calls:     functions_called,
	}
	return counter, instr, nil
}

func comp_load_func(counter uint64, code string, ctx compCtx) (uint64, instruction, error) {
	instr := instruction{instr: 9}
	counter++

	if counter >= uint64(len(code)) || code[counter] != '(' {
		return counter, instr, showError(ctx, code, counter-1, "error",
			"expected '(' after '?' to open function name")
	}
	counter++ // skip '('

	n_start := counter
	for counter < uint64(len(code)) && code[counter] != ')' {
		if counter+1 < uint64(len(code)) && code[counter] == '/' && code[counter+1] == '/' {
			for counter < uint64(len(code)) && code[counter] != '\n' {
				counter++
			}
			continue
		}
		counter++
	}
	if counter >= uint64(len(code)) {
		return counter, instr, showErrorWithNote(ctx, code,
			counter, "function call name never closed with ')'",
			n_start, "name started here",
		)
	}
	instr.f_name = code[n_start:counter]
	if err := validateName(instr.f_name, n_start, ctx, code); err != nil {
		return counter, instr, err
	}
	called_functs = append(called_functs, callSite{
		name:       instr.f_name,
		nameOffset: n_start,
		ctx:        ctx,
		code:       code,
	})

	counter++ // skip ')'
	return counter, instr, nil
}

// comp_case returns: new counter, case key, function name, name's start offset, error.
func comp_case(counter uint64, code string, ctx compCtx) (uint64, byte, string, uint64, error) {
	caseStart := counter
	counter++

	if counter >= uint64(len(code)) || code[counter] < '0' || code[counter] > '9' {
		return counter, 0, "", 0, showError(ctx, code, caseStart, "error",
			"expected a numeric value after '*' for case")
	}

	temp := string(code[counter])
	counter++
	for counter < uint64(len(code)) && code[counter] >= '0' && code[counter] <= '9' {
		temp += string(code[counter])
		counter++
	}
	out, _ := strconv.ParseUint(temp, 10, 64)
	if out > 255 {
		return counter, 0, "", 0, showError(ctx, code, caseStart+1, "error",
			fmt.Sprintf("case value %d overflows a byte (max 255)", out))
	}
	key_value := byte(out)

	if counter >= uint64(len(code)) || code[counter] != '(' {
		return counter, key_value, "", 0, showError(ctx, code, counter, "error",
			"expected '(' to open function name for case")
	}
	counter++ // skip '('

	n_start := counter
	for counter < uint64(len(code)) && code[counter] != ')' {
		if counter+1 < uint64(len(code)) && code[counter] == '/' && code[counter+1] == '/' {
			for counter < uint64(len(code)) && code[counter] != '\n' {
				counter++
			}
			continue
		}
		counter++
	}
	if counter >= uint64(len(code)) {
		return counter, key_value, "", 0, showErrorWithNote(ctx, code,
			counter, "case function name never closed with ')'",
			n_start, "name started here",
		)
	}
	name := code[n_start:counter]
	if err := validateName(name, n_start, ctx, code); err != nil {
		return counter, key_value, "", 0, err
	}
	counter++ // skip ')'
	return counter, key_value, name, n_start, nil
}

func comp_switch(counter uint64, code string, ctx compCtx) (uint64, instruction, error) {
	instr := instruction{instr: 10, switches: make(map[byte]string)}
	counter++

	if counter < uint64(len(code)) && code[counter] == '(' {
		counter++ // skip '('
		n_start := counter
		for counter < uint64(len(code)) && code[counter] != ')' && code[counter] != '{' {
			if counter+1 < uint64(len(code)) && code[counter] == '/' && code[counter+1] == '/' {
				for counter < uint64(len(code)) && code[counter] != '\n' {
					counter++
				}
				continue
			}
			counter++
		}
		if counter >= uint64(len(code)) {
			return counter, instr, showErrorWithNote(ctx, code,
				counter, "switch default name never closed with ')'",
				n_start, "name started here",
			)
		}
		if code[counter] == '{' {
			return counter, instr, showErrorWithNote(ctx, code,
				counter, "unexpected '{' inside switch default name — missing ')'",
				n_start, "name started here",
			)
		}
		instr.f_name = code[n_start:counter]
		if err := validateName(instr.f_name, n_start, ctx, code); err != nil {
			return counter, instr, err
		}
		called_functs = append(called_functs, callSite{
			name:       instr.f_name,
			nameOffset: n_start,
			ctx:        ctx,
			code:       code,
		})
		instr.value = 1
		counter++ // skip ')'
	}

	if counter >= uint64(len(code)) || code[counter] != '{' {
		return counter, instr, showError(ctx, code, counter, "error",
			"expected '{' to open switch body")
	}
	bodyOpen := counter
	counter++ // skip '{'

	for counter < uint64(len(code)) && code[counter] != '}' {
		if counter+1 < uint64(len(code)) && code[counter] == '/' && code[counter+1] == '/' {
			for counter < uint64(len(code)) && code[counter] != '\n' {
				counter++
			}
			continue
		}
		if code[counter] == '*' {
			c, k, n, nOff, err := comp_case(counter, code, ctx)
			if err != nil {
				return c, instr, err
			}
			instr.switches[k] = n
			called_functs = append(called_functs, callSite{
				name:       n,
				nameOffset: nOff,
				ctx:        ctx,
				code:       code,
			})
			counter = c
		} else {
			counter++
		}
	}
	if counter >= uint64(len(code)) {
		return counter, instr, showErrorWithNote(ctx, code,
			counter, "switch body never closed with '}'",
			bodyOpen, "body opened here",
		)
	}
	counter++ // skip '}'
	return counter, instr, nil
}

func comp_create_tape(counter uint64, code string, ctx compCtx) (uint64, instruction, error) {
	instr := instruction{instr: 12}
	counter++

	if counter >= uint64(len(code)) || code[counter] != '(' {
		return counter, instr, showError(ctx, code, counter-1, "error",
			"expected '(' after '§' to open tape name")
	}
	counter++ // skip '('

	n_start := counter
	for counter < uint64(len(code)) && code[counter] != ')' {
		if counter+1 < uint64(len(code)) && code[counter] == '/' && code[counter+1] == '/' {
			for counter < uint64(len(code)) && code[counter] != '\n' {
				counter++
			}
			continue
		}
		counter++
	}
	if counter >= uint64(len(code)) {
		return counter, instr, showErrorWithNote(ctx, code,
			counter, "tape name never closed with ')'",
			n_start, "name started here",
		)
	}
	instr.f_name = code[n_start:counter]
	if err := validateName(instr.f_name, n_start, ctx, code); err != nil {
		return counter, instr, err
	}
	if instr.f_name == "main" {
		return counter, instr, showErrorWithNote(ctx, code,
			counter, "tape name cannot be 'main'",
			n_start, "name started here",
		)
	}

	TAPES[instr.f_name] = tape_t{name: instr.f_name}
	counter++ // skip ')'
	return counter, instr, nil
}

func comp_switch_tape(counter uint64, code string, ctx compCtx) (uint64, instruction, error) {
	instr := instruction{instr: 13}
	counter++

	if counter >= uint64(len(code)) || code[counter] != '(' {
		instr.value = 0
		return counter, instr, nil
	}
	counter++ // skip '('

	n_start := counter
	for counter < uint64(len(code)) && code[counter] != ')' {
		if counter+1 < uint64(len(code)) && code[counter] == '/' && code[counter+1] == '/' {
			for counter < uint64(len(code)) && code[counter] != '\n' {
				counter++
			}
			continue
		}
		counter++
	}
	if counter >= uint64(len(code)) {
		return counter, instr, showErrorWithNote(ctx, code,
			counter, "tape name never closed with ')'",
			n_start, "name started here",
		)
	}
	instr.f_name = code[n_start:counter]
	if err := validateName(instr.f_name, n_start, ctx, code); err != nil {
		return counter, instr, err
	}
	if instr.f_name == "main" {
		instr.value = 0
	} else {
		instr.value = 1
		called_tapes = append(called_tapes, callSite{
			name:       instr.f_name,
			nameOffset: n_start,
			ctx:        ctx,
			code:       code,
		})
	}

	counter++ // skip ')'
	return counter, instr, nil
}

// comp_push compiles ^ (copy) and ^^ (move).
// isMove == true when the leading character was already identified as ^^.
// On entry counter points at the first '^'.
func comp_push(counter uint64, code string, ctx compCtx) (uint64, instruction, error) {
	startOff := counter
	isMove := false
	counter++ // skip first '^'

	if counter < uint64(len(code)) && code[counter] == '^' {
		isMove = true
		counter++ // skip second '^'
	}

	// read optional numeric width
	var width uint64 = 1
	if counter < uint64(len(code)) && code[counter] >= '1' && code[counter] <= '9' {
		temp := string(code[counter])
		counter++
		for counter < uint64(len(code)) && code[counter] >= '0' && code[counter] <= '9' {
			temp += string(code[counter])
			counter++
		}
		var err error
		width, err = strconv.ParseUint(temp, 10, 64)
		if err != nil || width == 0 || width > 255 {
			return counter, instruction{}, showError(ctx, code, startOff, "error",
				fmt.Sprintf("push width must be 1–255, got %s", temp))
		}
	}

	op := uint8(14) // copy
	if isMove {
		op = 15 // move
	}
	return counter, instruction{instr: op, value: width}, nil
}

// comp_pop compiles 'v' — no argument.
func comp_pop(counter uint64) (uint64, instruction) {
	counter++
	return counter, instruction{instr: 16}
}

func comp_extern_func(counter uint64, code string, ctx compCtx) (uint64, []sigSlot, []sigSlot, error) {
	var argSlots []sigSlot
	var retSlots []sigSlot

	// skip whitespace
	for counter < uint64(len(code)) && (code[counter] == ' ' || code[counter] == '\t') {
		counter++
	}

	if counter < uint64(len(code)) && code[counter] == '(' {
		// has arg signature → function mode
		var err error
		counter, argSlots, err = parseSigSlots(counter, code, ctx)
		if err != nil {
			return counter, argSlots, retSlots, err
		}

		// skip whitespace
		for counter < uint64(len(code)) && (code[counter] == ' ' || code[counter] == '\t') {
			counter++
		}

		// optional return signature
		if counter < uint64(len(code)) && code[counter] == '(' {
			counter, retSlots, err = parseSigSlots(counter, code, ctx)
			if err != nil {
				return counter, argSlots, retSlots, err
			}
		}
		// if no return parens, retSlots stays nil → no returns
	}

	return counter, argSlots, retSlots, nil
}

func comp_extern(counter uint64, code string, ctx compCtx) (uint64, []instruction, error) {
	counter++ // skip '@'
	instuc := instruction{instr: 17, value: 0}
	if counter < uint64(len(code)) {
		switch code[counter] {
		case '0':
			instuc.value = 0
			counter++
		case '1':
			instuc.value = 1
			counter++
		case '2':
			instuc.value = 2
			counter++
		case '3':
			instuc.value = 3
			counter++
		}
	}

	if counter >= uint64(len(code)) || code[counter] != '(' {
		return counter, []instruction{}, showError(ctx, code, counter-1, "error",
			fmt.Sprintf("@ expected '(' but got %c", code[counter]))
	}

	counter++
	n_start := counter
	for counter < uint64(len(code)) && code[counter] != ')' {
		if counter+1 < uint64(len(code)) && code[counter] == '/' && code[counter+1] == '/' {
			for counter < uint64(len(code)) && code[counter] != '\n' {
				counter++
			}
			continue
		}
		counter++
	}

	if counter >= uint64(len(code)) {
		return counter, []instruction{instuc}, showErrorWithNote(ctx, code,
			counter, "import name never closed with ')'",
			n_start, "import name started here",
		)
	}

	name := code[n_start:counter]
	counter++

	i, _ := IMPORTED[name]
	if i {
		return counter, []instruction{instuc}, showErrorWithNote(ctx, code,
			counter, "import name already imported",
			n_start, "import name started here",
		)
	}
	IMPORTED[name] = true
	instuc.f_name = name

	// if instuc.value != 0 {
	// 	return counter, []instruction{}, showError(ctx, code,
	// 		counter, "error",
	// 		"import type not yet supported",
	// 	)
	// }

	instructions := []instruction{instuc}

	if instuc.value == 0 {
		content, err := os.ReadFile(name + ".bfpp")
		if err != nil {
			return counter, []instruction{}, showError(ctx, code,
				counter, "error",
				"Import failed: "+err.Error(),
			)
		}

		src := string(content)
		childCtx := compCtx{
			globalSrc:  src,
			baseOffset: 0,
			funcName:   name + ".bfpp",
		}

		new_bytecode, _, err := make_bytecode(src, childCtx)
		if err != nil {
			showContext(ctx, name+".bfpp")
			return counter, []instruction{instuc}, err
		}
		instructions = append(instructions, new_bytecode...)
	} else if instuc.value == 1 {
		LIB_PATH = append(LIB_PATH, name)
	} else if instuc.value == 2 {
		HEADER = append(HEADER, name)
	} else if instuc.value == 3 {
		var err error
		var externFunc extern = extern{name: name}
		var args []sigSlot
		var results []sigSlot
		counter, args, results, err = comp_extern_func(counter, code, ctx)
		if err != nil {
			return counter, []instruction{}, err
		}
		externFunc.args = args
		externFunc.returns = results
		EXTERN[name] = externFunc
	}

	return counter, instructions, nil
}
