package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

var funcs []string
var macros_to_declare []string
var macros map[string]bool
var declared_funcs map[string]bool

// ── C backend ─────────────────────────────────────────────────────────────────

// callsSelf reports whether name is reachable from itself by following the
// call graph (functions_called), i.e. whether name is part of a call cycle.
func callsSelf(name string) bool {
	visited := make(map[string]bool)
	queue := append([]string{}, FUNCTS[name].calls...)
	for len(queue) > 0 {
		call := queue[0]
		queue = queue[1:]
		if call == name {
			return true
		}
		if visited[call] {
			continue
		}
		visited[call] = true
		queue = append(queue, FUNCTS[call].calls...)
	}
	return false
}

func compile_to_c(bytecode []instruction, out_name string) {
	var sb strings.Builder
	funcs = make([]string, 0)
	macros_to_declare = make([]string, 0)
	macros = make(map[string]bool)
	declared_funcs = make(map[string]bool)

	
	sb.WriteString("#include <stdlib.h>\n")
	sb.WriteString("#include <stdio.h>\n")
	sb.WriteString("#include <stdint.h>\n")
	sb.WriteString("#include <string.h>\n")


	for _, header := range HEADER {
		sb.WriteString(fmt.Sprintf("#include %s\n", header))
	}
	sb.WriteString("\n")

	if runtime.GOOS == "windows" {
		sb.WriteString("#include <windows.h>\n\n")

		sb.WriteString("DWORD orig_mode;\n")
		sb.WriteString("HANDLE hStdin;\n\n")

		sb.WriteString("void term_init() {\n")
		sb.WriteString("  hStdin = GetStdHandle(STD_INPUT_HANDLE);\n")
		sb.WriteString("  GetConsoleMode(hStdin, &orig_mode);\n\n")
		sb.WriteString("  DWORD raw_mode = orig_mode;\n")
		sb.WriteString("  raw_mode &= ~(ENABLE_LINE_INPUT | ENABLE_ECHO_INPUT);\n")
		sb.WriteString("  raw_mode &= ~ENABLE_QUICK_EDIT_MODE;\n\n")
		sb.WriteString("  SetConsoleMode(hStdin, raw_mode);\n")
		sb.WriteString("}\n\n")

		sb.WriteString("void term_restore() {\n")
		sb.WriteString("  SetConsoleMode(hStdin, orig_mode);\n")
		sb.WriteString("}\n\n")
	} else {

		sb.WriteString("#include <termios.h>\n")
		sb.WriteString("#include <unistd.h>\n\n")

		sb.WriteString("struct termios orig_term;\n\n")

		sb.WriteString("void term_init() {\n")
		sb.WriteString("  struct termios raw;\n")
		sb.WriteString("  tcgetattr(STDIN_FILENO, &orig_term);\n")
		sb.WriteString("  raw = orig_term;\n")
		sb.WriteString("  raw.c_lflag &= ~(ICANON | ECHO);\n")
		sb.WriteString("  raw.c_cc[VMIN] = 1;\n")
		sb.WriteString("  raw.c_cc[VTIME] = 0;\n")
		sb.WriteString("  tcsetattr(STDIN_FILENO, TCSANOW, &raw);\n")
		sb.WriteString("}\n\n")

		sb.WriteString("void term_restore() {\n")
		sb.WriteString("  tcsetattr(STDIN_FILENO, TCSAFLUSH, &orig_term);\n")
		sb.WriteString("}\n\n")
	}

	if *interpreter_debug {
		sb.WriteString("void PrintArray(uint8_t *data_arr, uint8_t data_length) {\n")
		sb.WriteString("    while (data_length--) {\n")
		sb.WriteString("        printf(\"0x%02x \", *data_arr);\n")
		sb.WriteString("        *data_arr++;\n")
		sb.WriteString("    }\n")
		sb.WriteString("    printf(\"\\n\");\n")
		sb.WriteString("}\n\n")
	}

	// ── stack struct ─────────────────────────────────────────────────────
	sb.WriteString("// bf++ value stack\n")
	sb.WriteString("typedef struct { uint8_t size; uint8_t data[255]; } BFEntry;\n")
	sb.WriteString("typedef struct { BFEntry entries[256]; uint8_t top; } BFStack;\n\n")

	sb.WriteString("static void bfstack_push(BFStack *s, uint8_t sz, uint8_t *bytes) {\n")
	sb.WriteString("  if (s->top == 255) { fprintf(stderr, \"runtime error: stack overflow\\n\"); exit(1); }\n")
	sb.WriteString("  s->entries[s->top].size = sz;\n")
	sb.WriteString("  memcpy(s->entries[s->top].data, bytes, sz);\n")
	if *interpreter_debug {
		sb.WriteString("  printf(\"pushed %d bytes\\n\", sz);\n")
		sb.WriteString("  PrintArray(s->entries[s->top].data, sz);\n")
	}
	sb.WriteString("  s->top++;\n")
	sb.WriteString("}\n\n")

	sb.WriteString("static BFEntry bfstack_pop(BFStack *s) {\n")
	sb.WriteString("  BFEntry zero = {1, {0}};\n")
	sb.WriteString("  if (s->top == 0) return zero;\n")
	sb.WriteString("  return s->entries[--s->top];\n")
	sb.WriteString("}\n\n")

	sb.WriteString("static void bfstack_transfer_args(BFStack *caller, BFStack *callee,\n")
	sb.WriteString("    uint8_t *sizes, int n, const char *fname) {\n")
	sb.WriteString("  for (int i = 0; i < n; i++) {\n")
	sb.WriteString("    BFEntry e = bfstack_pop(caller);\n")
	sb.WriteString("    if (sizes[i] != 0 && e.size != sizes[i]) {\n")
	sb.WriteString("      fprintf(stderr, \"runtime error: '%s' arg %d: expected %d-byte, got %d-byte\\n\",\n")
	sb.WriteString("              fname, i+1, (int)sizes[i], (int)e.size); exit(1);\n")
	sb.WriteString("    }\n")
	sb.WriteString("    bfstack_push(callee, e.size, e.data);\n")
	sb.WriteString("  }\n")
	sb.WriteString("}\n\n")

	sb.WriteString("static void bfstack_transfer_rets(BFStack *callee, BFStack *caller,\n")
	sb.WriteString("    uint8_t *sizes, int n, const char *fname) {\n")
	sb.WriteString("  BFEntry buf[256];\n")
	sb.WriteString("  for (int i = 0; i < n; i++) {\n")
	sb.WriteString("    BFEntry e = bfstack_pop(callee);\n")
	sb.WriteString("    if (sizes[i] != 0 && e.size != sizes[i]) {\n")
	sb.WriteString("      fprintf(stderr, \"runtime error: '%s' ret %d: expected %d-byte, got %d-byte\\n\",\n")
	sb.WriteString("              fname, i+1, (int)sizes[i], (int)e.size); exit(1);\n")
	sb.WriteString("    }\n")
	sb.WriteString("    buf[i] = e;\n")
	sb.WriteString("  }\n")
	sb.WriteString("  for (int i = n-1; i >= 0; i--) {\n")
	sb.WriteString("    bfstack_push(caller, buf[i].size, buf[i].data);\n")
	sb.WriteString("  }\n")
	sb.WriteString("}\n\n")

	// ── tapes ────────────────────────────────────────────────────────────
	for name := range TAPES {
		sb.WriteString(fmt.Sprintf("uint8_t tape_%s[0xffff] = {0};\n", name))
		sb.WriteString(fmt.Sprintf("uint16_t ptr_%s = 0;\n", name))
	}

	sb.WriteString("\n")

	sb.WriteString("uint8_t (*reg)[0xffff] = &tape_main;\n")
	sb.WriteString("uint16_t *ptr = &ptr_main;\n")

	sb.WriteString("\n")

	// ── forward declarations ──────────────────────────────────────────────


	for name, fn := range FUNCTS {
		if !fn.isMacro {
			funcs = append(funcs, name)
		} else if len(fn.calls) != 0 && callsSelf(name) {
			// A macro that (directly or transitively) calls itself can't be
			// expanded as a #define — the C preprocessor won't re-expand a
			// macro inside its own expansion, so it must become a real
			// function instead (still isMacro: no own stack).
			funcs = append(funcs, name)
		} else {
			macros[name] = true
			macros_to_declare = append(macros_to_declare, name)
		}
	}
	declared_funcs = make(map[string]bool)
	
	for _, name := range funcs {
		declared_funcs[name] = true
		if FUNCTS[name].isMacro {
			sb.WriteString(fmt.Sprintf("void f_%s(BFStack *stack);\n", name))
		} else {
			sb.WriteString(fmt.Sprintf("void f_%s(BFStack *caller_stack);\n", name))
		}
	}
	for name := range EXTERN {
		sb.WriteString(fmt.Sprintf("void f_%s(BFStack *caller_stack);\n", name))
		declared_funcs[name] = true
	}

	macro_scans := 0
	for len(macros_to_declare) > 0 && macro_scans < 10 {
		write_macro_declarations(&sb)
		i := 0
		for i < len(macros_to_declare) {
			if declared_funcs[macros_to_declare[i]] {
				macros_to_declare = append(macros_to_declare[:i], macros_to_declare[i+1:]...)
			} else {
				i++
			}
		}
		macro_scans++
	}
	for _, name := range macros_to_declare {
		sb.WriteString(fmt.Sprintf("void f_%s(BFStack *stack);\n", name))
		declared_funcs[name] = true
	}
	
	sb.WriteString("\n")

	// ── extern function bodies ───────────────────────────────────────────────────
	write_extern_wrappers(&sb)

	// ── function bodies ───────────────────────────────────────────────────
	for _, name := range funcs {
		fn := FUNCTS[name]
		
		if fn.isMacro {
			sb.WriteString(fmt.Sprintf("\nvoid f_%s(BFStack *stack) {\n", name))
			write_c_body(&sb, fn.instructs, "    ", true, false)
			sb.WriteString("}\n")
		} else {
			sb.WriteString(fmt.Sprintf("\nvoid f_%s(BFStack *caller_stack) {\n", name))
			sb.WriteString("  BFStack *own_stack = (BFStack*)calloc(1,sizeof(BFStack));\n")
			// sb.WriteString("  *own_stack_ptr = BFStack{0};\n")
			//sb.WriteString("  BFStack *own_stack = own_stack_ptr;\n")

			//sb.WriteString("  BFStack own_stack = {0};\n")
			// arg transfer
			if len(fn.args) > 0 {
				sb.WriteString("  { uint8_t _arg_sizes[] = {")
				for i, s := range fn.args {
					if i > 0 {
						sb.WriteString(", ")
					}
					if s.wildcard {
						sb.WriteString("0")
					} else {
						sb.WriteString(fmt.Sprintf("%d", s.exactSize))
					}
				}
				sb.WriteString("};\n")
				sb.WriteString(fmt.Sprintf("    bfstack_transfer_args(caller_stack, own_stack, _arg_sizes, %d, \"%s\"); }\n",
					len(fn.args), name))
			}


			write_c_body(&sb, fn.instructs, "    ", false, false)

			// return transfer
			if len(fn.returns) > 0 {
				sb.WriteString("  { uint8_t _ret_sizes[] = {")
				for i, s := range fn.returns {
					if i > 0 {
						sb.WriteString(", ")
					}
					if s.wildcard {
						sb.WriteString("0")
					} else {
						sb.WriteString(fmt.Sprintf("%d", s.exactSize))
					}
				}
				sb.WriteString("};\n")
				sb.WriteString(fmt.Sprintf("    bfstack_transfer_rets(own_stack, caller_stack, _ret_sizes, %d, \"%s\"); }\n",
					len(fn.returns), name))
			}
			
			sb.WriteString("  free(own_stack);\n")
			sb.WriteString("}\n")
		}
	}

	// ── main ──────────────────────────────────────────────────────────────
	sb.WriteString("\nint main() {\n")
	sb.WriteString("  term_init();\n")
	sb.WriteString("  BFStack root_stack = {0};\n")
	sb.WriteString("  f_main(&root_stack);\n")
	sb.WriteString("  term_restore();\n")
	sb.WriteString("  printf(\"\\n\");\n")
	sb.WriteString("  return 0;\n}\n")

	os.WriteFile(out_name+".c", []byte(sb.String()), 0644)

	if *output != "" {
		if err := invoke_compiler(*output); err != nil {
			fmt.Fprintln(os.Stderr, "error: compilation failed:", err)
		}
	}
	if !*print_bycode {
		os.Remove(out_name + ".c")
	}
}

func write_macro_declarations(sb *strings.Builder) {
	for name, _ := range macros {
		if declared_funcs[name] {
			continue
		}
		function_struct := FUNCTS[name]
		use_undefined := false
		for _, call := range function_struct.calls {
			if _, ok := declared_funcs[call]; !ok {
				use_undefined = true
			}
		}
		if !use_undefined {
			sb.WriteString(fmt.Sprintf("#define f_%s(stack) \\\n", name))
			write_c_body(sb, function_struct.instructs, "    ", true, true)
			sb.WriteString("\n//\n")
			declared_funcs[name] = true
		}
	}
}


func write_extern_wrappers(sb *strings.Builder) {
	for name, ext := range EXTERN {
		sb.WriteString(fmt.Sprintf("\nvoid f_%s(BFStack *caller_stack) {\n", name))
		sb.WriteString("    BFStack own_stack = {0};\n")

		// arg transfer
		if len(ext.args) > 0 {
			sb.WriteString("    { uint8_t _arg_sizes[] = {")
			for i, s := range ext.args {
				if i > 0 {
					sb.WriteString(", ")
				}
				if s.wildcard {
					sb.WriteString("0")
				} else {
					sb.WriteString(fmt.Sprintf("%d", s.exactSize))
				}
			}
			sb.WriteString(fmt.Sprintf("};\n        bfstack_transfer_args(caller_stack, &own_stack, _arg_sizes, %d, \"%s\"); }\n",
				len(ext.args), name))
		}

		// pop args and declare typed locals
		for i := len(ext.args) - 1; i >= 0; i-- {
			s := ext.args[i]
			sb.WriteString(fmt.Sprintf("    BFEntry _ae%d = bfstack_pop(&own_stack);\n", i))
			// sb.WriteString(fmt.Sprintf("  %s _a%d;\n", externCTypeName(s.type_), i))
			if s.type_ == "str" {
				sb.WriteString(fmt.Sprintf("    char *_a%d = (char*)_ae%d.data;\n", i, i))
			} else {
				sb.WriteString(fmt.Sprintf("    %s _a%d;\n", externCTypeName(s.type_), i))
				sb.WriteString(fmt.Sprintf("    memcpy(&_a%d, _ae%d.data, sizeof(%s));\n", i, i, externCTypeName(s.type_)))
			}
		}

		// build call
		var callArgs []string
		for i := range ext.args {
			callArgs = append(callArgs, fmt.Sprintf("_a%d", i))
		}
		callExpr := fmt.Sprintf("%s(%s)", name, strings.Join(callArgs, ", "))

		// emit call + push return
		if len(ext.returns) == 0 {
			sb.WriteString(fmt.Sprintf("    %s;\n", callExpr))
		} else if len(ext.returns) == 1 {
			cName := externCTypeName(ext.returns[0].type_)
			sb.WriteString(fmt.Sprintf("    %s _r0 = %s;\n", cName, callExpr))
			sb.WriteString(fmt.Sprintf("    bfstack_push(caller_stack, sizeof(%s), (uint8_t*)&_r0);\n", cName))
		} else {
			sb.WriteString(fmt.Sprintf("    // TODO: multiple return values for extern '%s'\n", name))
			sb.WriteString(fmt.Sprintf("    %s;\n", callExpr))
		}

		sb.WriteString("}\n")
	}
}



func externCTypeName(t string) string {
	if len(t) > 1 {
		switch t[0] {
		case 'u':
			return fmt.Sprintf("uint%s_t", t[1:])
		case 'i':
			return fmt.Sprintf("int%s_t", t[1:])
		case 'f':
			if t[1:] == "64" {
				return "double"
			}
			return "float"
		}
	}
	if t == "str" {
		return "char*"
	}
	if t == "bool" {
		return "_Bool"
	}
	return t // struct name as-is
}


func write_c_body(sb *strings.Builder, bytecode []instruction, indent string, isMacro bool, isTrueMacro bool) {
	// Which stack variable name are we operating on?
	// Macro functions receive BFStack *stack directly.
	// Function-mode bodies use their own local &own_stack.
	stackVar := "stack"
	if !isMacro {
		stackVar = "own_stack"
	}
	var text_end string
	if isTrueMacro {
		text_end = " \\\n"
	} else {
		text_end = "\n"
	}
	
	for _, instr := range bytecode {
		switch instr.instr {
		case 0:
			sb.WriteString(fmt.Sprintf("%s(*reg)[*ptr] += %d;%s", indent, instr.value, text_end))
		case 1:
			sb.WriteString(fmt.Sprintf("%s(*reg)[*ptr] -= %d;%s", indent, instr.value, text_end))
		case 2:
			sb.WriteString(fmt.Sprintf("%s*ptr -= %d;%s", indent, instr.value, text_end))
		case 3:
			sb.WriteString(fmt.Sprintf("%s*ptr += %d;%s", indent, instr.value, text_end))
		case 4:
			sb.WriteString(fmt.Sprintf("%swhile ((*reg)[*ptr]) {%s", indent, text_end))
			indent += "    "
		case 5:
			indent = indent[4:]
			sb.WriteString(fmt.Sprintf("%s}%s", indent, text_end))
		case 6:
			switch instr.value {
			case 0:
				sb.WriteString(fmt.Sprintf("%sputchar((*reg)[*ptr]);%s", indent, text_end))
			case 1:
				sb.WriteString(fmt.Sprintf("%sprintf(\"%%d\\0\", (*reg)[*ptr]);%s", indent, text_end))
			case 2:
				sb.WriteString(fmt.Sprintf("%sprintf(\"%%02x\\0\", (*reg)[*ptr]);%s", indent, text_end))
			case 3:
				sb.WriteString(fmt.Sprintf("%sprintf(\"%%08b\\0\", (*reg)[*ptr]);%s", indent, text_end))
			case 4:
				if *print_bycode{
					sb.WriteString(fmt.Sprintf("%sprintf(\"ptr pos: %%d\\n\\0\", *ptr);%s", indent, text_end))
				}
			}
		case 7:
			sb.WriteString(fmt.Sprintf("%s(*reg)[*ptr] = getchar();%s", indent, text_end))
		case 9:
			callee := FUNCTS[instr.f_name]
			if callee.isMacro {
				sb.WriteString(fmt.Sprintf("%sf_%s(%s);%s", indent, instr.f_name, stackVar, text_end))
			} else {
				sb.WriteString(fmt.Sprintf("%sf_%s(%s);%s", indent, instr.f_name, stackVar, text_end))
			}
		case 10:
			sb.WriteString(fmt.Sprintf("%sswitch ((*reg)[*ptr]) {%s", indent, text_end))
			for k, v := range instr.switches {
				callee := FUNCTS[v]
				if callee.isMacro {
					sb.WriteString(fmt.Sprintf("%s    case %d: f_%s(%s); break;%s", indent, k, v, stackVar, text_end))
				} else {
					sb.WriteString(fmt.Sprintf("%s    case %d: f_%s(%s); break;%s", indent, k, v, stackVar, text_end))
				}
			}
			if instr.value == 1 {
				callee := FUNCTS[instr.f_name]
				if callee.isMacro {
					sb.WriteString(fmt.Sprintf("%s    default: f_%s(%s); break;%s", indent, instr.f_name, stackVar, text_end))
				} else {
					sb.WriteString(fmt.Sprintf("%s    default: f_%s(%s); break;%s", indent, instr.f_name, stackVar, text_end))
				}
			}
			sb.WriteString(fmt.Sprintf("%s}%s", indent, text_end))
		case 13:
			if instr.value == 0 {
				sb.WriteString(fmt.Sprintf("%sreg = &tape_main;%s", indent, text_end))
				sb.WriteString(fmt.Sprintf("%sptr = &ptr_main;%s", indent, text_end))
			} else {
				sb.WriteString(fmt.Sprintf("%sreg = &tape_%s;%s", indent, instr.f_name, text_end))
				sb.WriteString(fmt.Sprintf("%sptr = &ptr_%s;%s", indent, instr.f_name, text_end))
			}
		// push copy
		case 14:
			sb.WriteString(fmt.Sprintf("%s{ bfstack_push(%s, %d, &(*reg)[*ptr]); }%s",
				indent, stackVar, instr.value, text_end))

		// push move
		case 15:
			sb.WriteString(fmt.Sprintf("%s{ bfstack_push(%s, %d, &(*reg)[*ptr]); %s", indent, stackVar, instr.value, text_end))
			sb.WriteString(fmt.Sprintf("%s    memset(&(*reg)[*ptr], 0, %d); }%s", indent, instr.value, text_end))

		// pop
		case 16:
			sb.WriteString(fmt.Sprintf("%s{ BFEntry _e = bfstack_pop(%s); %s", indent, stackVar, text_end))
			sb.WriteString(fmt.Sprintf("%s    memcpy(&(*reg)[*ptr], _e.data, _e.size); }%s", indent, text_end))

		}
	}
}

func invoke_compiler(out_name string) error {
	O_flag := fmt.Sprintf("-O%d",*optimization)
	args := []string{O_flag, "-o", out_name, out_name + ".c"}
	args = append(args, LIB_PATH...)
	if *compiler_args != "" {
		args = append(args, strings.Fields(*compiler_args)...)
	}
	fmt.Printf("%s %s\n", *compiler, strings.Join(args, " "))
	cmd := exec.Command(*compiler, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
