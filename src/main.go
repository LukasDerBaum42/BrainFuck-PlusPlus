package main
 
import (
	"flag"
	"fmt"
	"os"
	// "os/exec"
	// "strconv"
	// "strings"
	// "runtime"
)

/*  0 (+) = add
 *  1 (-) = sub
 *  2 (<) = move left
 *  3 (>) = move right
 *  4 ([) = start loop
 *  5 (]) = end loop
 *  6 (.) = print |  0 / none = ascii , 1 = int , 2 = hex , 3 = bin
 *  7 (,) = read
 *  8 (!) = create func
 *  9 (?) = call func
 * 10 ($) = switch
 * 11 (*) = case
 * 12 (§) = create tape
 * 13 (:) = switch tape
 * 14 (^) = push stack (copy)
 * 15 (^^) = push stack (move)
 * 16 (v) = pop stack
 * 17 (@) = extern | 0 / none = other bf++ program , 1 = c lib file , 2 = c header file , 3 = c function
 *
 * technicly keywords but not id: ( ) { } //
 */

// ── entry point ───────────────────────────────────────────────────────────────

var print_bycode = flag.Bool("d", false, "print bytecode")
var interpret = flag.Bool("i", false, "interpret the file of given name")
var interpreter_debug = flag.Bool("id", false, "interpreter debug info (only when interpreting)")
var show_loop = flag.Bool("l", false, "show loop count (only when the -id flag is given)")
var output = flag.String("o", "", "compile to output file")
var optimization = flag.Int("O", 2, "Optimization level 0 to 3")
var compiler = flag.String("c", "gcc", "a C compiler to use")
var compiler_args = flag.String("cargs", "", "C compiler arguments")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: %s [flags] <file>

At least one of -i, -o, or -d must be given.

Flags:
`, os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `

`)
	}

	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: no input file given")
		flag.Usage()
		return
	}
	if !*interpret && *output == "" && !*print_bycode {
		fmt.Fprintln(os.Stderr, "error: nothing to do — use at least one of -i, -o, -d")
		flag.Usage()
		return
	}

	content, err := os.ReadFile(args[len(args)-1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: could not read file:", err)
		return
	}

	src := string(content)
	FUNCTS = make(map[string]function)
	TAPES = make(map[string]tape_t)
	TAPES["main"] = tape_t{name: "main", data: [0xFFFF + 1]byte{}, position: 0}
	IMPORTED = make(map[string]bool)
	EXTERN = make(map[string]extern)
	HEADER = make([]string, 0)
	LIB_PATH = make([]string, 0)

	called_functs = make([]callSite, 0)
	called_tapes = make([]callSite, 0)

	rootCtx := compCtx{globalSrc: src, baseOffset: 0, funcName: ""}
	bytecode, _, err := make_bytecode(src, rootCtx)
	if err != nil {
		os.Exit(1)
	}

	// Verify every referenced function and tape was actually defined,
	// and point at the exact call site if not.
	not_found := false
	for _, cs := range called_functs {
		if _, ok1 := FUNCTS[cs.name]; !ok1 {
			if _, ok2 := EXTERN[cs.name]; !ok2 {
				showError(cs.ctx, cs.code, cs.nameOffset, "error",
					fmt.Sprintf("function '%s' is called here but never defined", cs.name))
				not_found = true
			}
		}
	}
	for _, cs := range called_tapes {
		if _, ok := TAPES[cs.name]; !ok {
			showError(cs.ctx, cs.code, cs.nameOffset, "error",
				fmt.Sprintf("tape '%s' is switched to here but never created", cs.name))
			not_found = true
		}
	}

	if not_found {
		os.Exit(1)
	}

	if *print_bycode {
		print_bytecode(bytecode, false)
		for i, ele := range FUNCTS {
			fmt.Println()
			fmt.Printf(":%s\n", i)
			print_bytecode(ele.instructs, true)
		}
	}

	if *interpret {
		start_func, ok := FUNCTS["main"]
		if !ok {
			fmt.Fprintln(os.Stderr, "error: no 'main' function defined")
			os.Exit(1)
		}
		termInit()
		defer termRestore()
		interpret_bytecode(start_func.instructs)
		fmt.Println()
	}

	if *output != "" {
		compile_to_c(bytecode, *output)
	}
}
